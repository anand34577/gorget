package gateway

import (
	"fmt"
	"net/netip"
	"sort"
	"strings"

	"github.com/anand34577/gorget/internal/core"
	"github.com/anand34577/gorget/internal/policy"
)

// BuildPFRuleset renders the gateway firewall for macOS packet filter (pf), the equivalent
// of BuildRuleset. It is loaded into an anchor, so it never touches the system rules.
//
// Everything entering from the tunnel interface is blocked unless a rule below allows it
// (stateful, so replies come back); nothing else is blocked. egress is the interface used
// for internet traffic (needed for NAT).
func BuildPFRuleset(iface, egress string, v *core.GatewayView) string {
	var b strings.Builder

	// NAT for exit traffic (translation rules must come first).
	v4src, v6src := splitFamilies(v.ExitSources)
	if egress != "" && len(v4src) > 0 {
		fmt.Fprintf(&b, "nat on %s inet from %s to any -> (%s)\n", egress, pfSet(v4src), egress)
	}
	_ = v6src // pf has no IPv6 NAT66 by default; IPv6 exit traffic must be routed, not translated

	// Input to the gateway itself: ICMP and DNS only.
	fmt.Fprintf(&b, "block in on %s all\n", iface)
	fmt.Fprintf(&b, "block out on %s all\n", iface)
	if v.Addr4.IsValid() {
		fmt.Fprintf(&b, "pass in quick on %s inet proto icmp to %s keep state\n", iface, v.Addr4)
		fmt.Fprintf(&b, "pass in quick on %s inet proto { tcp, udp } to %s port 53 keep state\n", iface, v.Addr4)
	}
	if v.IPv6On && v.Addr6.IsValid() {
		fmt.Fprintf(&b, "pass in quick on %s inet6 proto icmp6 to %s keep state\n", iface, v.Addr6)
		fmt.Fprintf(&b, "pass in quick on %s inet6 proto { tcp, udp } to %s port 53 keep state\n", iface, v.Addr6)
	}
	// Forwarded flows the policy allows.
	for _, line := range pfRuleLines(iface, v.Forward) {
		b.WriteString(line + "\n")
	}
	return b.String()
}

// pfRuleLines renders the allow rules. Rules with exclusions (internet rules, which
// must not reach the overlay or private subnet routes) become "block quick" for the
// excluded ranges followed by a pass. pf stops at the first quick match, so those
// pairs go after all plain rules: an exclusion then only blocks traffic that no
// other rule allows, matching the nftables semantics.
func pfRuleLines(iface string, rules []policy.FirewallRule) []string {
	seen := map[string]bool{}
	var plain, excluded []string
	add := func(dst *[]string, line string) {
		if !seen[line] {
			seen[line] = true
			*dst = append(*dst, line)
		}
	}
	for _, r := range rules {
		for _, fam := range []string{"ip", "ip6"} {
			src, dst := family(r.Src, fam), family(r.Dst, fam)
			if len(src) == 0 || len(dst) == 0 {
				continue
			}
			pfFam := "inet"
			if fam == "ip6" {
				pfFam = "inet6"
			}
			from, to := "any", "any"
			if !isAll(src, fam) {
				from = pfSet(src)
			}
			if !isAll(dst, fam) {
				to = pfSet(dst)
			}
			proto := pfProto(r, fam)
			pass := fmt.Sprintf("pass in quick on %s %s%s from %s to %s keep state", iface, pfFam, proto, from, to)
			if excl := family(r.Exclude, fam); len(excl) > 0 {
				add(&excluded, fmt.Sprintf("block in quick on %s %s%s from %s to %s", iface, pfFam, proto, from, pfSet(excl)))
				add(&excluded, pass)
				continue
			}
			add(&plain, pass)
		}
	}
	return append(plain, excluded...)
}

func pfProto(r policy.FirewallRule, fam string) string {
	allPorts := len(r.Ports) == 0 || (len(r.Ports) == 1 && r.Ports[0] == policy.AllPorts[0])
	ports := ""
	if !allPorts {
		var items []string
		for _, p := range r.Ports {
			if p.First == p.Last {
				items = append(items, fmt.Sprint(p.First))
			} else {
				items = append(items, fmt.Sprintf("%d:%d", p.First, p.Last))
			}
		}
		ports = " port { " + strings.Join(items, ", ") + " }"
	}
	switch r.Proto {
	case "icmp":
		if fam == "ip" {
			return " proto icmp"
		}
		return " proto icmp6"
	case "tcp", "udp":
		return " proto " + r.Proto + ports
	}
	if allPorts {
		return ""
	}
	return " proto { tcp, udp }" + ports
}

func pfSet(ps []netip.Prefix) string {
	ps = collapse(ps)
	items := make([]string, 0, len(ps))
	seen := map[string]bool{}
	for _, p := range ps {
		s := p.String()
		if p.IsSingleIP() {
			s = p.Addr().String()
		}
		if !seen[s] {
			seen[s] = true
			items = append(items, s)
		}
	}
	sort.Strings(items)
	return "{ " + strings.Join(items, ", ") + " }"
}
