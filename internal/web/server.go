// Package web is Gorget's built-in web server: TLS (automatic Let's Encrypt),
// HTTP/2 and HTTP/3, security headers, compression, client-IP handling and
// routing between the web console, REST API, client RPC API and relay.
package web

import (
	"context"
	"crypto/tls"
	"errors"
	"fmt"
	"github.com/caddyserver/certmagic"
	"io/fs"
	"log/slog"
	"net"
	"net/http"
	"net/netip"
	"path"
	"strings"
	"time"

	"github.com/klauspost/compress/gzhttp"
	"github.com/quic-go/quic-go/http3"

	"github.com/anand34577/gorget"
	"github.com/anand34577/gorget/internal/config"
	"github.com/anand34577/gorget/internal/web/ui"
)

type Server struct {
	cfg     config.Config
	log     *slog.Logger
	TLS     *TLSManager
	trusted []netip.Prefix
	adminOK []netip.Prefix
	// OverlayPrefixes returns the VPN ranges (for admin_only_from_vpn).
	OverlayPrefixes func() []netip.Prefix
}

// New creates the web server. storage (optional) is shared certificate storage for clusters.
func New(cfg config.Config, log *slog.Logger, storage certmagic.Storage) (*Server, error) {
	tm, err := NewTLSManager(cfg, log, storage)
	if err != nil {
		return nil, fmt.Errorf("tls: %w", err)
	}
	s := &Server{cfg: cfg, log: log, TLS: tm}
	for _, c := range cfg.HTTP.TrustedProxies {
		s.trusted = append(s.trusted, netip.MustParsePrefix(c))
	}
	for _, c := range cfg.HTTP.AdminAllowCIDRs {
		s.adminOK = append(s.adminOK, netip.MustParsePrefix(c))
	}
	return s, nil
}

// ClientIP returns the real client address, honouring trusted proxies only.
func (s *Server) ClientIP(r *http.Request) string {
	if v := r.Header.Get(clientIPHeader); v != "" && r.Context().Value(ipSetKey{}) != nil {
		return v
	}
	return s.resolveIP(r)
}

const clientIPHeader = "X-Gorget-Client-IP"

type ipSetKey struct{}

func (s *Server) resolveIP(r *http.Request) string {
	host, _, err := net.SplitHostPort(r.RemoteAddr)
	if err != nil {
		host = r.RemoteAddr
	}
	remote, err := netip.ParseAddr(host)
	if err != nil {
		return host
	}
	remote = remote.Unmap()
	if !s.isTrusted(remote) {
		return remote.String()
	}
	// Walk X-Forwarded-For from the right, skipping trusted proxies.
	if xff := r.Header.Get("X-Forwarded-For"); xff != "" {
		parts := strings.Split(xff, ",")
		for i := len(parts) - 1; i >= 0; i-- {
			a, err := netip.ParseAddr(strings.TrimSpace(parts[i]))
			if err != nil {
				break
			}
			a = a.Unmap()
			if !s.isTrusted(a) {
				return a.String()
			}
		}
	}
	if xr := r.Header.Get("X-Real-IP"); xr != "" {
		if a, err := netip.ParseAddr(strings.TrimSpace(xr)); err == nil {
			return a.Unmap().String()
		}
	}
	return remote.String()
}

func (s *Server) isTrusted(a netip.Addr) bool {
	for _, p := range s.trusted {
		if p.Contains(a) {
			return true
		}
	}
	return false
}

// Routes bundles the handlers mounted on the main listener.
type Routes struct {
	API     http.Handler // mounted at /api/v1/
	SCIM    http.Handler // mounted at /scim/v2/ (identity-provider provisioning; own bearer token)
	RPCPath string
	RPC     http.Handler
	Relay   http.Handler
	Ready   func(ctx context.Context) error
}

// Handler builds the root HTTP handler.
func (s *Server) Handler(rt Routes) http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("GET /healthz", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/plain")
		_, _ = w.Write([]byte("ok"))
	})
	mux.HandleFunc("GET /readyz", func(w http.ResponseWriter, r *http.Request) {
		ctx, cancel := context.WithTimeout(r.Context(), 3*time.Second)
		defer cancel()
		if err := rt.Ready(ctx); err != nil {
			http.Error(w, "not ready: "+err.Error(), http.StatusServiceUnavailable)
			return
		}
		_, _ = w.Write([]byte("ready"))
	})
	if ca := s.TLS.CACertPEM(); ca != nil {
		mux.HandleFunc("GET /ca.crt", func(w http.ResponseWriter, r *http.Request) {
			w.Header().Set("Content-Type", "application/x-x509-ca-cert")
			w.Header().Set("Content-Disposition", `attachment; filename="gorget-ca.crt"`)
			_, _ = w.Write(ca)
		})
	}
	compress := gzhttp.GzipHandler
	mux.Handle("/api/v1/events", s.admin(http.StripPrefix("/api/v1", rt.API)))
	mux.Handle("/api/v1/", s.admin(compress(http.StripPrefix("/api/v1", rt.API))))
	mux.Handle(rt.RPCPath, rt.RPC)
	if rt.SCIM != nil {
		mux.Handle("/scim/v2/", http.StripPrefix("/scim/v2", rt.SCIM))
	}
	if rt.Relay != nil {
		mux.Handle("/relay", rt.Relay)
	}
	// One-line client installer for Linux and macOS, with this server's address filled in:
	//   curl -fsSL https://vpn.example.com/install.sh | sh
	// Public on purpose (devices fetch it before they are on the network).
	// Windows: irm https://vpn.example.com/install.ps1 | iex
	serveScript := func(ctype, body string) http.Handler {
		return compress(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			w.Header().Set("Content-Type", ctype)
			w.Header().Set("Cache-Control", "no-cache")
			_, _ = w.Write([]byte(body))
		}))
	}
	mux.Handle("GET /install.sh", serveScript("text/x-shellscript; charset=utf-8", gorget.InstallScript(s.cfg.PublicURL)))
	mux.Handle("GET /install.ps1", serveScript("text/plain; charset=utf-8", gorget.InstallPowerShell(s.cfg.PublicURL)))
	mux.Handle("/", s.admin(compress(s.spa())))
	return s.base(mux)
}

// base applies client-IP resolution, security headers and access logging.
func (s *Server) base(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		ip := s.resolveIP(r)
		// Never trust an incoming copy of our internal header.
		r.Header.Set(clientIPHeader, ip)
		r = r.WithContext(context.WithValue(r.Context(), ipSetKey{}, true))
		if s.cfg.TLS.Mode == config.TLSModeOff && r.Header.Get("X-Forwarded-Proto") == "" && s.isTrusted(mustAddr(r.RemoteAddr)) {
			// Behind a proxy that didn't set the scheme: assume public URL scheme.
			if strings.HasPrefix(s.cfg.PublicURL, "https://") {
				r.Header.Set("X-Forwarded-Proto", "https")
			}
		}
		h := w.Header()
		h.Set("X-Content-Type-Options", "nosniff")
		h.Set("Referrer-Policy", "no-referrer")
		h.Set("X-Frame-Options", "DENY")
		h.Set("Permissions-Policy", "camera=(), microphone=(), geolocation=(), payment=(), usb=()")
		h.Set("Cross-Origin-Opener-Policy", "same-origin")
		h.Set("Cross-Origin-Resource-Policy", "same-origin")
		if r.TLS != nil || strings.HasPrefix(s.cfg.PublicURL, "https://") {
			h.Set("Strict-Transport-Security", "max-age=63072000; includeSubDomains")
		}
		if s.cfg.HTTP.AccessLog {
			start := time.Now()
			rw := &statusWriter{ResponseWriter: w, status: 200}
			next.ServeHTTP(rw, r)
			s.log.Info("http", "method", r.Method, "path", r.URL.Path, "status", rw.status, "dur", time.Since(start).Round(time.Millisecond), "ip", anonymise(ip))
			return
		}
		next.ServeHTTP(w, r)
	})
}

func mustAddr(hostport string) netip.Addr {
	host, _, err := net.SplitHostPort(hostport)
	if err != nil {
		host = hostport
	}
	a, _ := netip.ParseAddr(host)
	return a.Unmap()
}

// anonymise zeroes the host part of client IPs in access logs.
func anonymise(ip string) string {
	a, err := netip.ParseAddr(ip)
	if err != nil {
		return ""
	}
	if a.Is4() {
		p, _ := a.Prefix(24)
		return p.Addr().String()
	}
	p, _ := a.Prefix(48)
	return p.Addr().String()
}

type statusWriter struct {
	http.ResponseWriter
	status int
}

func (w *statusWriter) WriteHeader(c int) { w.status = c; w.ResponseWriter.WriteHeader(c) }
func (w *statusWriter) Flush() {
	if f, ok := w.ResponseWriter.(http.Flusher); ok {
		f.Flush()
	}
}
func (w *statusWriter) Unwrap() http.ResponseWriter { return w.ResponseWriter }

// admin restricts the console and admin API to allowed networks when configured.
func (s *Server) admin(next http.Handler) http.Handler {
	if len(s.adminOK) == 0 && !s.cfg.Security.AdminOnlyFromVPN {
		return next
	}
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		ip, err := netip.ParseAddr(r.Header.Get(clientIPHeader))
		if err == nil {
			if ip.IsLoopback() {
				next.ServeHTTP(w, r)
				return
			}
			allowed := append([]netip.Prefix{}, s.adminOK...)
			if s.cfg.Security.AdminOnlyFromVPN && s.OverlayPrefixes != nil {
				allowed = append(allowed, s.OverlayPrefixes()...)
			}
			for _, p := range allowed {
				if p.Contains(ip) {
					next.ServeHTTP(w, r)
					return
				}
			}
		}
		http.Error(w, "the admin console is not reachable from your network", http.StatusForbidden)
	})
}

const csp = "default-src 'self'; script-src 'self'; style-src 'self' 'unsafe-inline'; img-src 'self' data: blob:; " +
	"font-src 'self' data:; connect-src 'self'; worker-src 'self' blob:; frame-ancestors 'none'; base-uri 'none'; form-action 'self'; object-src 'none'"

// spa serves the embedded React console with index.html fallback.
func (s *Server) spa() http.Handler {
	dist, err := fs.Sub(ui.Files, "dist")
	if err != nil {
		panic(err)
	}
	files := http.FileServer(http.FS(dist))
	index, _ := fs.ReadFile(dist, "index.html")
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Security-Policy", csp)
		p := strings.TrimPrefix(path.Clean(r.URL.Path), "/")
		if p != "" && p != "index.html" {
			if st, err := fs.Stat(dist, p); err == nil && !st.IsDir() {
				if strings.HasPrefix(p, "assets/") {
					w.Header().Set("Cache-Control", "public, max-age=31536000, immutable")
				} else {
					w.Header().Set("Cache-Control", "public, max-age=3600")
				}
				files.ServeHTTP(w, r)
				return
			}
			if strings.HasPrefix(p, "assets/") {
				http.NotFound(w, r)
				return
			}
		}
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		w.Header().Set("Cache-Control", "no-cache")
		_, _ = w.Write(index)
	})
}

// Serve runs the HTTP(S), redirect and HTTP/3 listeners until ctx is done.
func (s *Server) Serve(ctx context.Context, h http.Handler) error {
	tlsCfg := s.TLS.TLSConfig()
	s.TLS.Start(ctx)

	srv := &http.Server{
		Addr:              s.cfg.HTTP.Listen,
		Handler:           h,
		TLSConfig:         tlsCfg,
		ReadHeaderTimeout: 10 * time.Second,
		IdleTimeout:       120 * time.Second,
		MaxHeaderBytes:    64 << 10,
		ErrorLog:          slog.NewLogLogger(s.log.Handler(), slog.LevelDebug),
	}
	var protos http.Protocols
	protos.SetHTTP1(true)
	protos.SetHTTP2(true)
	if tlsCfg == nil {
		protos.SetUnencryptedHTTP2(true) // h2c for gRPC-style clients behind a TLS proxy
	}
	srv.Protocols = &protos

	var h3 *http3.Server
	if s.cfg.HTTP.HTTP3 && tlsCfg != nil {
		h3cfg := tlsCfg.Clone()
		h3cfg.MinVersion = tls.VersionTLS13
		h3cfg.NextProtos = []string{http3.NextProtoH3}
		h3 = &http3.Server{Addr: s.cfg.HTTP.Listen, Handler: h, TLSConfig: http3.ConfigureTLSConfig(h3cfg), IdleTimeout: 120 * time.Second}
		inner := srv.Handler
		srv.Handler = http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			_ = h3.SetQUICHeaders(w.Header())
			inner.ServeHTTP(w, r)
		})
	}

	errc := make(chan error, 3)
	ln, err := net.Listen("tcp", s.cfg.HTTP.Listen)
	if err != nil {
		return fmt.Errorf("listen %s: %w", s.cfg.HTTP.Listen, err)
	}
	if tlsCfg != nil {
		ln = tls.NewListener(ln, tlsCfg)
	}
	s.log.Info("web server listening", "addr", s.cfg.HTTP.Listen, "tls", s.cfg.TLS.Mode, "public_url", s.cfg.PublicURL)
	go func() { errc <- srv.Serve(ln) }()
	if h3 != nil {
		go func() {
			if err := h3.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
				s.log.Warn("HTTP/3 listener failed; continuing with HTTP/1.1 and HTTP/2", "err", err)
			}
		}()
	}

	var redirect *http.Server
	if s.cfg.HTTP.RedirectListen != "" && tlsCfg != nil {
		redirect = &http.Server{
			Addr:              s.cfg.HTTP.RedirectListen,
			Handler:           s.TLS.HTTPChallengeHandler(http.HandlerFunc(s.redirectHTTPS)),
			ReadHeaderTimeout: 5 * time.Second,
			IdleTimeout:       30 * time.Second,
		}
		go func() {
			if err := redirect.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
				s.log.Warn("HTTP redirect listener failed (ACME HTTP-01 challenges will not work)", "addr", s.cfg.HTTP.RedirectListen, "err", err)
			}
		}()
	}

	select {
	case <-ctx.Done():
	case err := <-errc:
		if err != nil && !errors.Is(err, http.ErrServerClosed) {
			return err
		}
	}
	shutdownCtx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	if redirect != nil {
		_ = redirect.Shutdown(shutdownCtx)
	}
	if h3 != nil {
		_ = h3.Close()
	}
	return srv.Shutdown(shutdownCtx)
}

func (s *Server) redirectHTTPS(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet && r.Method != http.MethodHead {
		http.Error(w, "use HTTPS", http.StatusBadRequest)
		return
	}
	target := s.cfg.PublicURL + r.URL.RequestURI()
	http.Redirect(w, r, target, http.StatusPermanentRedirect)
}
