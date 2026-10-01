// Package localapi is the daemon's control interface: JSON over HTTP on a Unix
// socket (Linux, macOS) or a named pipe (Windows). Anyone on the computer may
// read status; changing anything needs root/Administrator or the configured
// operator. Browsers can't reach sockets or pipes, so no CSRF layer is needed.
package localapi

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"net/http"
	"net/netip"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/anand34577/gorget/client"
	"github.com/anand34577/gorget/client/filetransfer"
)

// Caller identifies the local user behind a connection.
type Caller struct {
	// Admin is true for root (Unix) or members of Administrators / SYSTEM (Windows).
	Admin bool
	// User is the account name, "" when unknown.
	User string
}

type callerKey struct{}

// FileStore is the inbox of received files.
type FileStore interface {
	List() []filetransfer.Incoming
	Open(name string) (*os.File, error)
	Delete(name string) error
}

// Server serves the local API for one client.
type Server struct {
	c       *client.Client
	dataDir string
	// Quit asks the daemon to exit.
	Quit func()
	// Files is the inbox of files received from the user's other devices (nil = off).
	Files FileStore
	// Bypass keeps a process outside the tunnel (nil = unsupported on this OS).
	Bypass func(pid int) error

	mu   sync.Mutex
	subs map[chan client.Status]struct{}
}

// New creates the server and subscribes it to status changes.
func New(c *client.Client, dataDir string) *Server {
	s := &Server{c: c, dataDir: dataDir, subs: map[chan client.Status]struct{}{}}
	c.OnStatus(s.broadcast)
	return s
}

func (s *Server) broadcast(st client.Status) {
	s.mu.Lock()
	defer s.mu.Unlock()
	for ch := range s.subs {
		select {
		case ch <- st:
		default: // slow reader: drop; the next change carries the full state anyway
		}
	}
}

func (s *Server) operatorFile() string { return filepath.Join(s.dataDir, "operator") }

// Operator returns the user allowed to control the daemon without being admin.
func (s *Server) Operator() string {
	b, err := os.ReadFile(s.operatorFile())
	if err != nil {
		return ""
	}
	return strings.TrimSpace(string(b))
}

// SetOperator stores the operator ("" clears it).
func SetOperator(dataDir, user string) error {
	p := filepath.Join(dataDir, "operator")
	if user == "" {
		err := os.Remove(p)
		if errors.Is(err, os.ErrNotExist) {
			return nil
		}
		return err
	}
	if err := os.MkdirAll(dataDir, 0o700); err != nil {
		return err
	}
	return os.WriteFile(p, []byte(user+"\n"), 0o644)
}

func (s *Server) canWrite(c Caller) bool {
	if c.Admin {
		return true
	}
	return operatorMatches(s.Operator(), c.User)
}

// operatorMatches compares the configured operator with the caller's account. An
// operator given as DOMAIN\user must match exactly; a bare name matches that user
// on this computer only (the caller's DOMAIN\ part must be this computer's name),
// so a same-named account from another domain doesn't get control.
func operatorMatches(op, user string) bool {
	if op == "" || user == "" {
		return false
	}
	if strings.EqualFold(op, user) {
		return true
	}
	if strings.Contains(op, "\\") {
		return false
	}
	dom, name, ok := strings.Cut(user, "\\")
	if !ok {
		return false // Unix names have no domain and were compared above
	}
	if !strings.EqualFold(name, op) {
		return false
	}
	host, _ := os.Hostname()
	// Windows reports local accounts under the NetBIOS computer name (COMPUTERNAME).
	for _, h := range []string{host, shortHostname(host), os.Getenv("COMPUTERNAME")} {
		if h != "" && strings.EqualFold(dom, h) {
			return true
		}
	}
	return false
}

func shortHostname(h string) string {
	h, _, _ = strings.Cut(h, ".")
	return h
}

// Serve accepts connections until ctx ends.
func (s *Server) Serve(ctx context.Context, ln net.Listener) error {
	srv := &http.Server{
		Handler:           s.routes(),
		ReadHeaderTimeout: 10 * time.Second,
		ConnContext: func(ctx context.Context, conn net.Conn) context.Context {
			return context.WithValue(ctx, callerKey{}, callerOf(conn))
		},
	}
	go func() {
		<-ctx.Done()
		sctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
		defer cancel()
		_ = srv.Shutdown(sctx)
		_ = srv.Close()
	}()
	err := srv.Serve(ln)
	if errors.Is(err, http.ErrServerClosed) {
		return nil
	}
	return err
}

type apiError struct {
	Error string `json:"error"`
}

func writeJSON(w http.ResponseWriter, code int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(code)
	_ = json.NewEncoder(w).Encode(v)
}

func fail(w http.ResponseWriter, code int, err error) { writeJSON(w, code, apiError{err.Error()}) }

func (s *Server) routes() http.Handler {
	mux := http.NewServeMux()
	// Reads: any local user.
	mux.HandleFunc("GET /v1/status", func(w http.ResponseWriter, r *http.Request) { writeJSON(w, 200, s.c.Status()) })
	mux.HandleFunc("GET /v1/prefs", func(w http.ResponseWriter, r *http.Request) { writeJSON(w, 200, s.c.Prefs()) })
	mux.HandleFunc("GET /v1/whoami", func(w http.ResponseWriter, r *http.Request) {
		c, _ := r.Context().Value(callerKey{}).(Caller)
		writeJSON(w, 200, WhoAmI{User: c.User, Admin: c.Admin, CanControl: s.canWrite(c), Operator: s.Operator(), Version: client.Version})
	})
	mux.HandleFunc("GET /v1/events", s.events)

	// Writes: root/admin or operator.
	write := func(h func(http.ResponseWriter, *http.Request)) http.HandlerFunc {
		return func(w http.ResponseWriter, r *http.Request) {
			c, _ := r.Context().Value(callerKey{}).(Caller)
			if !s.canWrite(c) {
				fail(w, http.StatusForbidden, errors.New("access denied: run as root/Administrator, or ask an administrator to make you the operator (gorget operator set <user>)"))
				return
			}
			r.Body = http.MaxBytesReader(w, r.Body, 1<<20)
			h(w, r)
		}
	}
	mux.HandleFunc("POST /v1/server", write(s.setServer))
	mux.HandleFunc("POST /v1/login", write(s.login))
	mux.HandleFunc("POST /v1/login/setup-key", write(s.loginSetupKey))
	mux.HandleFunc("POST /v1/up", write(func(w http.ResponseWriter, r *http.Request) { s.done(w, s.c.Up()) }))
	mux.HandleFunc("POST /v1/down", write(func(w http.ResponseWriter, r *http.Request) { s.c.Down(); s.done(w, nil) }))
	mux.HandleFunc("POST /v1/logout", write(func(w http.ResponseWriter, r *http.Request) { s.done(w, s.c.Logout(r.Context())) }))
	mux.HandleFunc("PATCH /v1/prefs", write(s.patchPrefs))
	mux.HandleFunc("POST /v1/app-bypass", write(s.appBypass))
	mux.HandleFunc("GET /v1/files", write(s.listFiles))
	mux.HandleFunc("GET /v1/files/{name}", write(s.getFile))
	mux.HandleFunc("DELETE /v1/files/{name}", write(s.deleteFile))
	mux.HandleFunc("POST /v1/quit", write(func(w http.ResponseWriter, r *http.Request) {
		s.done(w, nil)
		if s.Quit != nil {
			go s.Quit()
		}
	}))
	return mux
}

func (s *Server) done(w http.ResponseWriter, err error) {
	if err != nil {
		fail(w, http.StatusBadRequest, err)
		return
	}
	writeJSON(w, 200, s.c.Status())
}

// WhoAmI describes the caller and its rights.
type WhoAmI struct {
	User       string `json:"user"`
	Admin      bool   `json:"admin"`
	CanControl bool   `json:"can_control"`
	Operator   string `json:"operator"`
	Version    string `json:"version"`
}

// LoginInfo is returned when a browser sign-in starts.
type LoginInfo struct {
	URL  string `json:"url"`
	Code string `json:"code"`
}

func (s *Server) setServer(w http.ResponseWriter, r *http.Request) {
	var in struct {
		URL string `json:"url"`
	}
	if json.NewDecoder(r.Body).Decode(&in) != nil {
		fail(w, 400, errors.New("invalid request"))
		return
	}
	if _, err := s.c.SetServer(r.Context(), in.URL); err != nil {
		fail(w, 400, err)
		return
	}
	writeJSON(w, 200, s.c.Status())
}

func (s *Server) login(w http.ResponseWriter, r *http.Request) {
	u, code, err := s.c.LoginInteractive(r.Context())
	if err != nil {
		fail(w, 400, err)
		return
	}
	writeJSON(w, 200, LoginInfo{URL: u, Code: code})
}

func (s *Server) loginSetupKey(w http.ResponseWriter, r *http.Request) {
	var in struct {
		Key string `json:"key"`
	}
	if json.NewDecoder(r.Body).Decode(&in) != nil || strings.TrimSpace(in.Key) == "" {
		fail(w, 400, errors.New("a setup key is required"))
		return
	}
	s.done(w, s.c.LoginWithSetupKey(r.Context(), in.Key))
}

// patchPrefs merges the JSON body onto the current preferences.
func (s *Server) patchPrefs(w http.ResponseWriter, r *http.Request) {
	p := s.c.Prefs()
	dec := json.NewDecoder(r.Body)
	dec.DisallowUnknownFields()
	if err := dec.Decode(&p); err != nil {
		fail(w, 400, fmt.Errorf("invalid preferences: %w", err))
		return
	}
	for _, rt := range p.AdvertiseRoutes {
		if _, err := netip.ParsePrefix(rt); err != nil {
			fail(w, 400, fmt.Errorf("%q is not a network like 192.168.1.0/24", rt))
			return
		}
	}
	if err := s.c.SetPrefs(p); err != nil {
		fail(w, 400, err)
		return
	}
	writeJSON(w, 200, s.c.Prefs())
}

// events streams the status as server-sent events (one JSON object per event).
func (s *Server) events(w http.ResponseWriter, r *http.Request) {
	fl, ok := w.(http.Flusher)
	if !ok {
		fail(w, 500, errors.New("streaming unsupported"))
		return
	}
	ch := make(chan client.Status, 8)
	s.mu.Lock()
	s.subs[ch] = struct{}{}
	s.mu.Unlock()
	defer func() {
		s.mu.Lock()
		delete(s.subs, ch)
		s.mu.Unlock()
	}()
	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("Cache-Control", "no-cache")
	send := func(st client.Status) bool {
		b, _ := json.Marshal(st)
		_, err := fmt.Fprintf(w, "data: %s\n\n", b)
		fl.Flush()
		return err == nil
	}
	if !send(s.c.Status()) {
		return
	}
	// Peer path details change without a status event, so refresh periodically too.
	tick := time.NewTicker(5 * time.Second)
	defer tick.Stop()
	for {
		select {
		case <-r.Context().Done():
			return
		case <-ch:
			// A burst of changes collapses into one send of the latest full state.
			for len(ch) > 0 {
				<-ch
			}
			if !send(s.c.Status()) {
				return
			}
		case <-tick.C:
			if !send(s.c.Status()) {
				return
			}
		}
	}
}

func (s *Server) listFiles(w http.ResponseWriter, r *http.Request) {
	if s.Files == nil {
		writeJSON(w, 200, []filetransfer.Incoming{})
		return
	}
	writeJSON(w, 200, s.Files.List())
}

func (s *Server) getFile(w http.ResponseWriter, r *http.Request) {
	if s.Files == nil {
		fail(w, http.StatusNotFound, errors.New("file receiving is off"))
		return
	}
	name := r.PathValue("name")
	f, err := s.Files.Open(name)
	if err != nil {
		fail(w, http.StatusNotFound, errors.New("no such file"))
		return
	}
	defer f.Close()
	st, err := f.Stat()
	if err != nil {
		fail(w, http.StatusNotFound, errors.New("no such file"))
		return
	}
	w.Header().Set("Content-Type", "application/octet-stream")
	w.Header().Set("Content-Disposition", "attachment; filename*=UTF-8''"+url.PathEscape(name))
	http.ServeContent(w, r, name, st.ModTime(), f)
}

func (s *Server) deleteFile(w http.ResponseWriter, r *http.Request) {
	if s.Files == nil {
		fail(w, http.StatusNotFound, errors.New("file receiving is off"))
		return
	}
	if err := s.Files.Delete(r.PathValue("name")); err != nil {
		fail(w, http.StatusNotFound, errors.New("no such file"))
		return
	}
	writeJSON(w, 200, map[string]bool{"ok": true})
}

func (s *Server) appBypass(w http.ResponseWriter, r *http.Request) {
	var in struct {
		PID int `json:"pid"`
	}
	if json.NewDecoder(r.Body).Decode(&in) != nil || in.PID <= 1 {
		fail(w, 400, errors.New("a process id is required"))
		return
	}
	if s.Bypass == nil {
		fail(w, 501, errors.New("per-app routing isn't available on this operating system (Linux only: Windows needs a signed kernel driver and macOS a network extension)"))
		return
	}
	if err := s.Bypass(in.PID); err != nil {
		fail(w, 500, err)
		return
	}
	writeJSON(w, 200, map[string]bool{"ok": true})
}
