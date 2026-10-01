package dnsserver

import (
	"context"
	"log/slog"
	"net/netip"
	"testing"

	"github.com/miekg/dns"
)

func TestLocalResolution(t *testing.T) {
	s := New(slog.Default())
	s.SetConfig(&Config{
		Domain: "gorget.internal",
		Hosts: map[string][]netip.Addr{
			"nas.gorget.internal": {netip.MustParseAddr("100.80.0.5"), netip.MustParseAddr("fd00::5")},
		},
		CNAMEs: map[string]string{"files.gorget.internal": "nas.gorget.internal"},
	})
	q := func(name string, t uint16) *dns.Msg {
		m := new(dns.Msg)
		m.SetQuestion(name, t)
		return s.Resolve(context.Background(), m)
	}
	r := q("nas.gorget.internal.", dns.TypeA)
	if len(r.Answer) != 1 || r.Answer[0].(*dns.A).A.String() != "100.80.0.5" {
		t.Fatalf("A: %v", r)
	}
	r = q("NAS.gorget.internal.", dns.TypeAAAA)
	if len(r.Answer) != 1 {
		t.Fatalf("AAAA: %v", r)
	}
	r = q("missing.gorget.internal.", dns.TypeA)
	if r.Rcode != dns.RcodeNameError {
		t.Fatalf("NXDOMAIN expected: %v", r)
	}
	r = q("files.gorget.internal.", dns.TypeA)
	if len(r.Answer) != 2 {
		t.Fatalf("CNAME chain: %v", r)
	}
	r = q("5.0.80.100.in-addr.arpa.", dns.TypePTR)
	if len(r.Answer) != 1 || r.Answer[0].(*dns.PTR).Ptr != "nas.gorget.internal." {
		t.Fatalf("PTR: %v", r)
	}
	// No upstreams: external names fail.
	r = q("example.com.", dns.TypeA)
	if r.Rcode != dns.RcodeServerFailure {
		t.Fatalf("expected SERVFAIL without upstreams: %v", r)
	}
}

func TestRecordsOutsideTheDomain(t *testing.T) {
	s := New(slog.Default())
	s.SetConfig(&Config{
		Domain: "gorget.internal",
		Hosts: map[string][]netip.Addr{
			"jellyfin.home.lan": {netip.MustParseAddr("192.168.1.20")},
			"*.apps.home.lan":   {netip.MustParseAddr("192.168.1.30")},
		},
	})
	ask := func(name string) *dns.Msg {
		m := new(dns.Msg)
		m.SetQuestion(name, dns.TypeA)
		return s.Resolve(context.Background(), m)
	}
	if r := ask("jellyfin.home.lan."); len(r.Answer) != 1 {
		t.Fatalf("custom record outside the domain: %v", r)
	}
	if r := ask("grafana.apps.home.lan."); len(r.Answer) != 1 {
		t.Fatalf("wildcard outside the domain: %v", r)
	}
	// Other names are forwarded (here: no upstreams, so SERVFAIL rather than NXDOMAIN).
	if r := ask("example.home.lan."); r.Rcode != dns.RcodeServerFailure {
		t.Fatalf("unknown names must be forwarded: %v", r)
	}
}

func TestWildcardRecords(t *testing.T) {
	s := New(slog.Default())
	s.SetConfig(&Config{
		Domain: "gorget.internal",
		Hosts: map[string][]netip.Addr{
			"*.apps.gorget.internal":   {netip.MustParseAddr("192.168.1.10")},
			"web.apps.gorget.internal": {netip.MustParseAddr("192.168.1.20")},
		},
	})
	ip := func(name string) string {
		m := new(dns.Msg)
		m.SetQuestion(name, dns.TypeA)
		r := s.Resolve(context.Background(), m)
		if len(r.Answer) != 1 {
			return dns.RcodeToString[r.Rcode]
		}
		return r.Answer[0].(*dns.A).A.String()
	}
	if got := ip("x.y.apps.gorget.internal."); got != "192.168.1.10" {
		t.Fatalf("wildcard: %s", got)
	}
	if got := ip("web.apps.gorget.internal."); got != "192.168.1.20" {
		t.Fatalf("exact must win: %s", got)
	}
	if got := ip("other.gorget.internal."); got != "NXDOMAIN" {
		t.Fatalf("outside the wildcard: %s", got)
	}
}
