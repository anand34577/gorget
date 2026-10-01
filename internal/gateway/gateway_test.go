package gateway

import (
	"net/netip"
	"strings"
	"testing"

	"github.com/anand34577/gorget/internal/core"
	"github.com/anand34577/gorget/internal/policy"
)

func TestBuildRuleset(t *testing.T) {
	p := netip.MustParsePrefix
	v := &core.GatewayView{
		Net4:        p("100.80.0.0/16"),
		ExitSources: []netip.Prefix{p("100.80.0.7/32"), p("fd00::7/128")},
		Forward: []policy.FirewallRule{
			{RuleID: "ssh", Src: []netip.Prefix{p("100.80.0.7/32")}, Dst: []netip.Prefix{p("100.80.0.4/32"), p("100.80.0.0/16")}, Ports: []policy.PortRange{{First: 22, Last: 22}, {First: 8000, Last: 8100}}, Proto: "tcp"},
			{RuleID: "inet", Src: []netip.Prefix{p("100.80.0.7/32")}, Dst: []netip.Prefix{p("0.0.0.0/0"), p("::/0")}, Exclude: []netip.Prefix{p("100.80.0.0/16"), p("192.168.1.0/24")}, Ports: policy.AllPorts},
			{RuleID: "any", Src: []netip.Prefix{p("0.0.0.0/0")}, Dst: []netip.Prefix{p("100.80.0.7/32")}, Ports: []policy.PortRange{{First: 53, Last: 53}}},
		},
	}
	got := BuildRuleset("gorget0", "", v)
	for _, want := range []string{
		"delete table inet gorget",
		`ip saddr { 100.80.0.7 } oifname != "gorget0" masquerade`,
		`ip6 saddr { fd00::7 } oifname != "gorget0" masquerade`,
		// Overlapping dst elements collapse to the covering prefix.
		`ip saddr { 100.80.0.7 } ip daddr { 100.80.0.0/16 } tcp dport { 22, 8000-8100 } accept comment "rule:ssh"`,
		`ip saddr { 100.80.0.7 } ip daddr != { 100.80.0.0/16, 192.168.1.0/24 } accept comment "rule:inet"`,
		`ip daddr { 100.80.0.7 } meta l4proto { tcp, udp } th dport { 53 } accept comment "rule:any"`,
		"\t\tdrop\n\t}",
	} {
		if !strings.Contains(got, want) {
			t.Errorf("ruleset missing %q\n%s", want, got)
		}
	}
	// IPv6 internet rule has no IPv6 source, so it must not appear.
	if strings.Contains(got, "ip6 daddr != ") {
		t.Errorf("unexpected ip6 rule:\n%s", got)
	}
}
