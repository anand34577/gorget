// Package dnsserver is Gorget's DNS resolver: it answers names in the network
// domain (MagicDNS + custom records), forwards split-DNS domains, and forwards
// everything else to upstreams over UDP/TCP, DNS-over-TLS or DNS-over-HTTPS.
package dnsserver

import (
	"bytes"
	"context"
	"crypto/tls"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net"
	"net/http"
	"net/netip"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/miekg/dns"
	"golang.org/x/sync/singleflight"
)

// Config is the resolver configuration. It can be swapped atomically at runtime.
type Config struct {
	Domain      string
	Hosts       map[string][]netip.Addr // lowercase FQDN without trailing dot
	CNAMEs      map[string]string
	Nameservers []string
	Split       map[string][]string // domain suffix -> nameservers
	// AllowFrom restricts which client source addresses may query (empty = any).
	AllowFrom []netip.Prefix
	FailOpen  bool
	TTL       uint32
}

type Server struct {
	log    *slog.Logger
	cfg    atomic.Pointer[Config]
	ptr    atomic.Pointer[map[string]string]
	cache  *cache
	dialer *net.Dialer
	doh    *http.Client
	srvMu  sync.Mutex
	srvs   []*dns.Server
	// inflight coalesces identical concurrent upstream queries into one.
	inflight singleflight.Group

	Queries   atomic.Uint64
	Forwarded atomic.Uint64
	Failures  atomic.Uint64
	hook      atomic.Pointer[AnswerHook]
}

func New(log *slog.Logger) *Server { return NewWithDialer(log, &net.Dialer{Timeout: 5 * time.Second}) }

// NewWithDialer uses d for all upstream connections (clients pass a dialer whose
// sockets bypass the VPN).
// OnAnswer is called with the addresses a query for name resolved to (after forwarding or
// from the cache). Used by clients for domain-based routing.
type AnswerHook func(name string, addrs []netip.Addr, ttl uint32)

// SetAnswerHook installs (or with nil removes) the answer hook.
func (s *Server) SetAnswerHook(h AnswerHook) {
	if h == nil {
		s.hook.Store(nil)
		return
	}
	s.hook.Store(&h)
}

func (s *Server) notify(name string, resp *dns.Msg) {
	hp := s.hook.Load()
	if hp == nil || resp == nil || resp.Rcode != dns.RcodeSuccess {
		return
	}
	var addrs []netip.Addr
	ttl := uint32(0)
	for _, rr := range resp.Answer {
		var a netip.Addr
		switch v := rr.(type) {
		case *dns.A:
			a, _ = netip.AddrFromSlice(v.A.To4())
		case *dns.AAAA:
			a, _ = netip.AddrFromSlice(v.AAAA)
		default:
			continue
		}
		if a.IsValid() {
			addrs = append(addrs, a)
			if t := rr.Header().Ttl; ttl == 0 || t < ttl {
				ttl = t
			}
		}
	}
	if len(addrs) > 0 {
		(*hp)(name, addrs, ttl)
	}
}

func NewWithDialer(log *slog.Logger, d *net.Dialer) *Server {
	s := &Server{
		log:    log,
		cache:  newCache(4096),
		dialer: d,
		doh: &http.Client{
			Timeout: 5 * time.Second,
			Transport: &http.Transport{
				DialContext:         d.DialContext,
				ForceAttemptHTTP2:   true,
				MaxIdleConnsPerHost: 4,
				IdleConnTimeout:     90 * time.Second,
				TLSClientConfig:     &tls.Config{MinVersion: tls.VersionTLS12},
			},
		},
	}
	s.SetConfig(&Config{TTL: 60})
	return s
}

// SetConfig replaces the configuration.
func (s *Server) SetConfig(c *Config) {
	if c.TTL == 0 {
		c.TTL = 60
	}
	c.Domain = strings.ToLower(strings.TrimSuffix(c.Domain, "."))
	ptr := map[string]string{}
	for name, addrs := range c.Hosts {
		for _, a := range addrs {
			if rev, err := dns.ReverseAddr(a.String()); err == nil {
				ptr[rev] = name + "."
			}
		}
	}
	s.cfg.Store(c)
	s.ptr.Store(&ptr)
	s.cache.clear()
}

// Listen starts UDP and TCP servers on addr (e.g. "100.80.0.1:53").
func (s *Server) Listen(addr string) error {
	s.srvMu.Lock()
	defer s.srvMu.Unlock()
	for _, network := range []string{"udp", "tcp"} {
		srv := &dns.Server{Addr: addr, Net: network, Handler: s, ReadTimeout: 5 * time.Second, WriteTimeout: 5 * time.Second}
		started := make(chan error, 1)
		srv.NotifyStartedFunc = func() { started <- nil }
		go func() {
			if err := srv.ListenAndServe(); err != nil {
				select {
				case started <- err:
				default:
					s.log.Warn("DNS server stopped", "addr", addr, "net", network, "err", err)
				}
			}
		}()
		select {
		case err := <-started:
			if err != nil {
				return fmt.Errorf("dns listen %s/%s: %w", addr, network, err)
			}
		case <-time.After(3 * time.Second):
			return fmt.Errorf("dns listen %s/%s: timeout", addr, network)
		}
		s.srvs = append(s.srvs, srv)
	}
	s.log.Info("DNS resolver listening", "addr", addr)
	return nil
}

func (s *Server) Shutdown() {
	s.srvMu.Lock()
	defer s.srvMu.Unlock()
	for _, srv := range s.srvs {
		_ = srv.Shutdown()
	}
	s.srvs = nil
}

func (s *Server) ServeDNS(w dns.ResponseWriter, req *dns.Msg) {
	s.Queries.Add(1)
	cfg := s.cfg.Load()
	if len(cfg.AllowFrom) > 0 && !allowed(cfg.AllowFrom, w.RemoteAddr()) {
		m := new(dns.Msg)
		m.SetRcode(req, dns.RcodeRefused)
		_ = w.WriteMsg(m)
		return
	}
	resp := s.Resolve(context.Background(), req)
	if _, isUDP := w.RemoteAddr().(*net.UDPAddr); isUDP {
		size := uint16(dns.MinMsgSize)
		if o := req.IsEdns0(); o != nil {
			size = o.UDPSize()
		}
		resp.Truncate(int(size))
	}
	_ = w.WriteMsg(resp)
}

func allowed(ps []netip.Prefix, a net.Addr) bool {
	var ip netip.Addr
	switch v := a.(type) {
	case *net.UDPAddr:
		ip = v.AddrPort().Addr().Unmap()
	case *net.TCPAddr:
		ip = v.AddrPort().Addr().Unmap()
	default:
		return false
	}
	for _, p := range ps {
		if p.Contains(ip) {
			return true
		}
	}
	return ip.IsLoopback()
}

// Resolve answers a query (exported for in-process use by clients).
func (s *Server) Resolve(ctx context.Context, req *dns.Msg) *dns.Msg {
	cfg := s.cfg.Load()
	if len(req.Question) != 1 {
		m := new(dns.Msg)
		m.SetRcode(req, dns.RcodeFormatError)
		return m
	}
	q := req.Question[0]
	name := strings.ToLower(strings.TrimSuffix(q.Name, "."))

	if m, ok := s.local(cfg, req, q, name); ok {
		return m
	}
	// The most specific split-DNS domain wins ("a.corp.example" over "corp.example").
	ups, best := cfg.Nameservers, -1
	for suffix, ns := range cfg.Split {
		if (name == suffix || strings.HasSuffix(name, "."+suffix)) && len(suffix) > best {
			ups, best = ns, len(suffix)
		}
	}
	if len(ups) == 0 && cfg.FailOpen {
		ups = systemResolvers()
	}
	if len(ups) == 0 {
		m := new(dns.Msg)
		m.SetRcode(req, dns.RcodeServerFailure)
		return m
	}
	key := cacheKey(q)
	if m := s.cache.get(key); m != nil {
		m.Id = req.Id
		s.notify(name, m)
		return m
	}
	// Identical questions asked at the same time (a browser opening many tabs, many
	// devices behind one gateway) share one upstream exchange.
	v, err, _ := s.inflight.Do(key+"|"+strings.Join(ups, ","), func() (any, error) {
		resp, err := s.forward(ctx, req, ups)
		if err != nil && cfg.FailOpen && best < 0 {
			// Fail open: the configured upstreams are unreachable, try the system's.
			if sys := systemResolvers(); len(sys) > 0 {
				resp, err = s.forward(ctx, req, sys)
			}
		}
		if err != nil {
			return nil, err
		}
		s.cache.put(key, resp)
		return resp, nil
	})
	if err != nil {
		s.Failures.Add(1)
		m := new(dns.Msg)
		m.SetRcode(req, dns.RcodeServerFailure)
		return m
	}
	s.Forwarded.Add(1)
	resp := v.(*dns.Msg).Copy() // shared between callers: each gets its own copy
	resp.Id = req.Id
	s.notify(name, resp)
	return resp
}

var (
	sysOnce sync.Once
	sysNS   []string
)

// systemResolvers returns the nameservers in /etc/resolv.conf (Unix), minus the
// in-tunnel resolver of a Gorget client, which would loop back to us.
func systemResolvers() []string {
	sysOnce.Do(func() {
		cc, err := dns.ClientConfigFromFile("/etc/resolv.conf")
		if err != nil {
			return
		}
		for _, srv := range cc.Servers {
			if srv == "100.100.100.100" || strings.HasPrefix(srv, "fd00:6764:6e73:") {
				continue
			}
			sysNS = append(sysNS, net.JoinHostPort(srv, cc.Port))
		}
	})
	return sysNS
}

func (s *Server) local(cfg *Config, req *dns.Msg, q dns.Question, name string) (*dns.Msg, bool) {
	m := new(dns.Msg)
	m.SetReply(req)
	m.Authoritative = true
	m.RecursionAvailable = true

	if q.Qtype == dns.TypePTR {
		if target, ok := (*s.ptr.Load())[strings.ToLower(q.Name)]; ok {
			m.Answer = append(m.Answer, &dns.PTR{Hdr: hdr(q.Name, dns.TypePTR, cfg.TTL), Ptr: target})
			return m, true
		}
		return nil, false
	}
	inDomain := cfg.Domain != "" && (name == cfg.Domain || strings.HasSuffix(name, "."+cfg.Domain))
	// Custom records may also name things outside the network domain
	// ("jellyfin.home.lan"); those are answered here and everything else is forwarded.
	if !inDomain {
		_, isCNAME := lookup(cfg.CNAMEs, name)
		_, isHost := lookup(cfg.Hosts, name)
		if !isCNAME && !isHost {
			return nil, false
		}
	}
	if target, ok := lookup(cfg.CNAMEs, name); ok {
		m.Answer = append(m.Answer, &dns.CNAME{Hdr: hdr(q.Name, dns.TypeCNAME, cfg.TTL), Target: dns.Fqdn(target)})
		if addrs, ok := lookup(cfg.Hosts, target); ok {
			m.Answer = append(m.Answer, addrRecords(dns.Fqdn(target), q.Qtype, addrs, cfg.TTL)...)
		}
		return m, true
	}
	addrs, ok := lookup(cfg.Hosts, name)
	if !ok {
		m.Rcode = dns.RcodeNameError
		m.Ns = append(m.Ns, soa(cfg.Domain, cfg.TTL))
		return m, true
	}
	m.Answer = addrRecords(q.Name, q.Qtype, addrs, cfg.TTL)
	if len(m.Answer) == 0 && inDomain {
		m.Ns = append(m.Ns, soa(cfg.Domain, cfg.TTL)) // NODATA
	}
	return m, true
}

// lookup finds an exact name, else the closest wildcard record ("*.apps.home.lan" answers
// "a.b.apps.home.lan"). Exact names always win, so device names are never shadowed.
func lookup[V any](m map[string]V, name string) (V, bool) {
	if v, ok := m[name]; ok {
		return v, true
	}
	for rest := name; ; {
		_, after, found := strings.Cut(rest, ".")
		if !found {
			var zero V
			return zero, false
		}
		if v, ok := m["*."+after]; ok {
			return v, true
		}
		rest = after
	}
}

func addrRecords(name string, qtype uint16, addrs []netip.Addr, ttl uint32) []dns.RR {
	var out []dns.RR
	for _, a := range addrs {
		switch {
		case a.Is4() && (qtype == dns.TypeA || qtype == dns.TypeANY):
			out = append(out, &dns.A{Hdr: hdr(name, dns.TypeA, ttl), A: a.AsSlice()})
		case a.Is6() && (qtype == dns.TypeAAAA || qtype == dns.TypeANY):
			out = append(out, &dns.AAAA{Hdr: hdr(name, dns.TypeAAAA, ttl), AAAA: a.AsSlice()})
		}
	}
	return out
}

func hdr(name string, t uint16, ttl uint32) dns.RR_Header {
	return dns.RR_Header{Name: dns.Fqdn(name), Rrtype: t, Class: dns.ClassINET, Ttl: ttl}
}

func soa(domain string, ttl uint32) dns.RR {
	return &dns.SOA{
		Hdr: hdr(domain, dns.TypeSOA, ttl), Ns: "ns." + dns.Fqdn(domain), Mbox: "hostmaster." + dns.Fqdn(domain),
		Serial: 1, Refresh: 3600, Retry: 600, Expire: 86400, Minttl: ttl,
	}
}

// forward tries each upstream in order.
func (s *Server) forward(ctx context.Context, req *dns.Msg, ups []string) (*dns.Msg, error) {
	var lastErr error
	for _, up := range ups {
		ctx, cancel := context.WithTimeout(ctx, 4*time.Second)
		resp, err := s.exchange(ctx, req, up)
		cancel()
		if err == nil && resp != nil && resp.Rcode != dns.RcodeServerFailure {
			return resp, nil
		}
		if err == nil {
			err = errors.New("upstream SERVFAIL")
		}
		lastErr = fmt.Errorf("%s: %w", up, err)
	}
	return nil, lastErr
}

func (s *Server) exchange(ctx context.Context, req *dns.Msg, up string) (*dns.Msg, error) {
	switch {
	case strings.HasPrefix(up, "https://"):
		return s.doHExchange(ctx, req, up)
	case strings.HasPrefix(up, "tls://"):
		hostport := strings.TrimPrefix(up, "tls://")
		host := hostport
		if h, _, err := net.SplitHostPort(hostport); err == nil {
			host = h
		} else {
			hostport = net.JoinHostPort(hostport, "853")
		}
		c := &dns.Client{Net: "tcp-tls", Timeout: 4 * time.Second, Dialer: s.dialer, TLSConfig: &tls.Config{ServerName: host, MinVersion: tls.VersionTLS12}}
		r, _, err := c.ExchangeContext(ctx, req, hostport)
		return r, err
	default:
		addr := up
		if _, _, err := net.SplitHostPort(up); err != nil {
			addr = net.JoinHostPort(up, "53")
		}
		c := &dns.Client{Net: "udp", Timeout: 3 * time.Second, Dialer: s.dialer}
		r, _, err := c.ExchangeContext(ctx, req, addr)
		if err == nil && r.Truncated {
			c.Net = "tcp"
			r, _, err = c.ExchangeContext(ctx, req, addr)
		}
		return r, err
	}
}

func (s *Server) doHExchange(ctx context.Context, req *dns.Msg, url string) (*dns.Msg, error) {
	q := req.Copy()
	q.Id = 0 // RFC 8484 recommends ID 0 for cache friendliness
	wire, err := q.Pack()
	if err != nil {
		return nil, err
	}
	hreq, err := http.NewRequestWithContext(ctx, http.MethodPost, url, bytes.NewReader(wire))
	if err != nil {
		return nil, err
	}
	hreq.Header.Set("Content-Type", "application/dns-message")
	hreq.Header.Set("Accept", "application/dns-message")
	resp, err := s.doh.Do(hreq)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("DoH HTTP %d", resp.StatusCode)
	}
	body, err := io.ReadAll(io.LimitReader(resp.Body, 65535))
	if err != nil {
		return nil, err
	}
	m := new(dns.Msg)
	if err := m.Unpack(body); err != nil {
		return nil, err
	}
	return m, nil
}

// ---------- cache ----------

type cacheEntry struct {
	msg     *dns.Msg
	expires time.Time
}

type cache struct {
	mu   sync.Mutex
	max  int
	data map[string]cacheEntry
}

func newCache(max int) *cache { return &cache{max: max, data: map[string]cacheEntry{}} }

func cacheKey(q dns.Question) string {
	return fmt.Sprintf("%s|%d|%d", strings.ToLower(q.Name), q.Qtype, q.Qclass)
}

func (c *cache) get(k string) *dns.Msg {
	c.mu.Lock()
	defer c.mu.Unlock()
	e, ok := c.data[k]
	if !ok {
		return nil
	}
	remaining := time.Until(e.expires)
	if remaining <= 0 {
		delete(c.data, k)
		return nil
	}
	m := e.msg.Copy()
	ttl := uint32(remaining.Seconds())
	for _, rr := range append(append(m.Answer, m.Ns...), m.Extra...) {
		if rr.Header().Rrtype != dns.TypeOPT {
			rr.Header().Ttl = ttl
		}
	}
	return m
}

func (c *cache) put(k string, m *dns.Msg) {
	if m.Rcode != dns.RcodeSuccess && m.Rcode != dns.RcodeNameError {
		return
	}
	ttl := uint32(300)
	for _, rr := range append(m.Answer, m.Ns...) {
		if t := rr.Header().Ttl; t < ttl {
			ttl = t
		}
	}
	if ttl == 0 {
		return
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	if len(c.data) >= c.max {
		now := time.Now()
		for key, e := range c.data {
			if now.After(e.expires) || len(c.data) >= c.max {
				delete(c.data, key)
			}
		}
	}
	c.data[k] = cacheEntry{msg: m.Copy(), expires: time.Now().Add(time.Duration(ttl) * time.Second)}
}

func (c *cache) clear() {
	c.mu.Lock()
	c.data = map[string]cacheEntry{}
	c.mu.Unlock()
}
