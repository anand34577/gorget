// Package policy implements Gorget's access-control policy: parsing,
// validation, compilation against the current set of nodes, and evaluation.
//
// Policy documents are HuJSON (JSON with comments and trailing commas):
//
//	{
//	  "tagOwners": { "tag:server": ["group:admins"] },
//	  "hosts":     { "nas": "192.168.1.10/32" },
//	  "acls": [
//	    { "action": "accept", "src": ["group:eng"], "dst": ["tag:server:22,443", "nas:*"] },
//	    { "action": "accept", "src": ["autogroup:member"], "dst": ["autogroup:internet:*"] }
//	  ],
//	  "autoApprovers": { "routes": { "192.168.0.0/16": ["tag:router"] }, "exitNode": ["tag:exit"] },
//	  "tests": [ { "src": "alice@example.com", "accept": ["tag:server:22"], "deny": ["nas:80"] } ]
//	}
//
// Selectors: "*", "<email>" or "user:<email>", "group:<name>", "tag:<name>",
// "device:<name>", "host:<alias>" or a bare alias, an IP or CIDR,
// "autogroup:member" (all user-owned devices), "autogroup:self" (dst only:
// devices of the same user as the source), "autogroup:internet" (dst only:
// internet access through exit nodes).
package policy

import (
	"encoding/json"
	"errors"
	"fmt"
	"net/netip"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/tailscale/hujson"
)

type Policy struct {
	TagOwners     map[string][]string `json:"tagOwners,omitempty"`
	Hosts         map[string]string   `json:"hosts,omitempty"`
	ACLs          []Rule              `json:"acls"`
	AutoApprovers AutoApprovers       `json:"autoApprovers,omitempty"`
	Tests         []Test              `json:"tests,omitempty"`
}

type Rule struct {
	ID          string   `json:"id,omitempty"`
	Description string   `json:"description,omitempty"`
	Action      string   `json:"action"`
	Src         []string `json:"src"`
	Dst         []string `json:"dst"`
	// Proto restricts the rule to one protocol: "tcp", "udp", "icmp" or empty for any.
	Proto    string `json:"proto,omitempty"`
	Disabled bool   `json:"disabled,omitempty"`
	// Expires is an RFC 3339 timestamp after which the rule no longer applies.
	Expires string `json:"expires,omitempty"`
}

type AutoApprovers struct {
	Routes   map[string][]string `json:"routes,omitempty"`
	ExitNode []string            `json:"exitNode,omitempty"`
}

type Test struct {
	Src    string   `json:"src"`
	Proto  string   `json:"proto,omitempty"`
	Accept []string `json:"accept,omitempty"`
	Deny   []string `json:"deny,omitempty"`
}

// DefaultAllowAll is the starter policy for personal networks.
const DefaultAllowAll = `{
  // Gorget access policy. Everything not explicitly allowed is denied.
  // Docs: selectors can be users (email), group:<name>, tag:<name>, device:<name>,
  // host aliases, IPs/CIDRs, autogroup:member, autogroup:self, autogroup:internet.
  "tagOwners": {},
  "hosts": {},
  "acls": [
    {
      "id": "allow-all",
      "description": "All devices can reach each other",
      "action": "accept",
      "src": ["*"],
      "dst": ["*:*"]
    },
    {
      "id": "internet",
      "description": "All devices may use exit nodes / full-tunnel internet access",
      "action": "accept",
      "src": ["*"],
      "dst": ["autogroup:internet:*"]
    }
  ],
  "autoApprovers": {
    "routes": {},
    "exitNode": []
  },
  "tests": []
}
`

// DefaultDeny is the starter policy for zero-trust setups.
const DefaultDeny = `{
  // Gorget access policy. Everything not explicitly allowed is denied.
  "tagOwners": {},
  "hosts": {},
  "acls": [
    {
      "id": "self",
      "description": "Users can reach their own devices",
      "action": "accept",
      "src": ["autogroup:member"],
      "dst": ["autogroup:self:*"]
    }
  ],
  "autoApprovers": {
    "routes": {},
    "exitNode": []
  },
  "tests": []
}
`

// Parse parses a HuJSON policy document and validates its syntax.
func Parse(doc string) (*Policy, error) {
	std, err := hujson.Standardize([]byte(doc))
	if err != nil {
		return nil, fmt.Errorf("syntax error: %w", err)
	}
	dec := json.NewDecoder(strings.NewReader(string(std)))
	dec.DisallowUnknownFields()
	var p Policy
	if err := dec.Decode(&p); err != nil {
		return nil, fmt.Errorf("invalid policy: %w", err)
	}
	if err := p.Validate(); err != nil {
		return nil, err
	}
	return &p, nil
}

// Format pretty-prints a policy document while keeping comments.
func Format(doc string) (string, error) {
	v, err := hujson.Parse([]byte(doc))
	if err != nil {
		return "", err
	}
	v.Format()
	return string(v.Pack()), nil
}

// ValidationError aggregates every problem found in a policy.
type ValidationError struct{ Problems []string }

func (e *ValidationError) Error() string {
	return "policy has " + strconv.Itoa(len(e.Problems)) + " problem(s): " + strings.Join(e.Problems, "; ")
}

func (p *Policy) Validate() error {
	var probs []string
	add := func(f string, a ...any) { probs = append(probs, fmt.Sprintf(f, a...)) }

	for tag, owners := range p.TagOwners {
		if !strings.HasPrefix(tag, "tag:") || !validName(strings.TrimPrefix(tag, "tag:")) {
			add("tagOwners: %q must look like tag:<name>", tag)
		}
		for _, o := range owners {
			if !strings.HasPrefix(o, "group:") && !strings.Contains(o, "@") && !strings.HasPrefix(o, "tag:") {
				add("tagOwners[%s]: owner %q must be a group, user email or tag", tag, o)
			}
		}
	}
	for alias, cidr := range p.Hosts {
		if !validName(alias) || strings.Contains(alias, ":") {
			add("hosts: invalid alias %q", alias)
		}
		if _, err := parsePrefix(cidr); err != nil {
			add("hosts[%s]: %v", alias, err)
		}
	}
	ids := map[string]bool{}
	for i, r := range p.ACLs {
		where := fmt.Sprintf("acls[%d]", i)
		if r.ID != "" {
			where = fmt.Sprintf("acls[%d] (%s)", i, r.ID)
			if ids[r.ID] {
				add("%s: duplicate id", where)
			}
			ids[r.ID] = true
		}
		if r.Action != "accept" {
			add("%s: action must be \"accept\" (everything else is denied by default)", where)
		}
		if len(r.Src) == 0 {
			add("%s: src is empty", where)
		}
		if len(r.Dst) == 0 {
			add("%s: dst is empty", where)
		}
		switch r.Proto {
		case "", "tcp", "udp", "icmp":
		default:
			add("%s: proto must be tcp, udp, icmp or empty", where)
		}
		if r.Expires != "" {
			if _, err := time.Parse(time.RFC3339, r.Expires); err != nil {
				add("%s: expires must be RFC 3339 (e.g. 2026-12-31T23:59:00Z)", where)
			}
		}
		for _, s := range r.Src {
			if err := p.checkSelector(s, false); err != nil {
				add("%s src %q: %v", where, s, err)
			}
		}
		for _, d := range r.Dst {
			sel, ports, err := splitDst(d)
			if err != nil {
				add("%s dst %q: %v", where, d, err)
				continue
			}
			if _, err := ParsePorts(ports); err != nil {
				add("%s dst %q: %v", where, d, err)
			}
			if err := p.checkSelector(sel, true); err != nil {
				add("%s dst %q: %v", where, d, err)
			}
		}
	}
	for cidr, sels := range p.AutoApprovers.Routes {
		if _, err := parsePrefix(cidr); err != nil {
			add("autoApprovers.routes[%s]: %v", cidr, err)
		}
		for _, s := range sels {
			if err := p.checkSelector(s, false); err != nil {
				add("autoApprovers.routes[%s] %q: %v", cidr, s, err)
			}
		}
	}
	for _, s := range p.AutoApprovers.ExitNode {
		if err := p.checkSelector(s, false); err != nil {
			add("autoApprovers.exitNode %q: %v", s, err)
		}
	}
	for i, t := range p.Tests {
		if t.Src == "" {
			add("tests[%d]: src is empty", i)
		}
		for _, d := range append(append([]string{}, t.Accept...), t.Deny...) {
			if _, _, err := splitTestDst(d); err != nil {
				add("tests[%d] %q: %v", i, d, err)
			}
		}
	}
	if len(probs) > 0 {
		sort.Strings(probs)
		return &ValidationError{Problems: probs}
	}
	return nil
}

func (p *Policy) checkSelector(s string, isDst bool) error {
	switch {
	case s == "*":
		return nil
	case s == "autogroup:member":
		return nil
	case s == "autogroup:self", s == "autogroup:internet":
		if !isDst {
			return errors.New("only allowed in dst")
		}
		return nil
	case strings.HasPrefix(s, "autogroup:"):
		return errors.New("unknown autogroup")
	case strings.HasPrefix(s, "group:"), strings.HasPrefix(s, "tag:"), strings.HasPrefix(s, "device:"):
		name := s[strings.Index(s, ":")+1:]
		if !validName(name) {
			return errors.New("invalid name")
		}
		return nil
	case strings.HasPrefix(s, "user:"):
		if !strings.Contains(s, "@") {
			return errors.New("user selector must be an email")
		}
		return nil
	case strings.HasPrefix(s, "host:"):
		if _, ok := p.Hosts[strings.TrimPrefix(s, "host:")]; !ok {
			return errors.New("unknown host alias")
		}
		return nil
	case strings.Contains(s, "@"):
		return nil
	}
	if _, err := parsePrefix(s); err == nil {
		return nil
	}
	if _, ok := p.Hosts[s]; ok {
		return nil
	}
	return errors.New("unknown selector (expected *, email, group:, tag:, device:, host alias, IP or CIDR)")
}

func validName(s string) bool {
	if s == "" || len(s) > 63 {
		return false
	}
	for _, c := range s {
		if !(c >= 'a' && c <= 'z' || c >= 'A' && c <= 'Z' || c >= '0' && c <= '9' || c == '-' || c == '_' || c == '.') {
			return false
		}
	}
	return true
}

// splitDst splits "selector:ports". IPv6 literals must be written in brackets: [fd00::1]:22.
func splitDst(d string) (sel, ports string, err error) {
	if strings.HasPrefix(d, "[") {
		end := strings.Index(d, "]")
		if end < 0 || end+1 >= len(d) || d[end+1] != ':' {
			return "", "", errors.New("IPv6 destinations must be written as [addr]:ports")
		}
		return d[1:end], d[end+2:], nil
	}
	i := strings.LastIndex(d, ":")
	if i <= 0 || i == len(d)-1 {
		return "", "", errors.New("destination must be selector:ports (use :* for all ports)")
	}
	sel, ports = d[:i], d[i+1:]
	if sel == "autogroup" || sel == "tag" || sel == "group" || sel == "device" || sel == "user" || sel == "host" {
		return "", "", errors.New("destination must be selector:ports (use :* for all ports)")
	}
	return sel, ports, nil
}

// splitTestDst splits "selector:port" where port is a single number.
func splitTestDst(d string) (string, uint16, error) {
	sel, ports, err := splitDst(d)
	if err != nil {
		return "", 0, err
	}
	n, err := strconv.ParseUint(ports, 10, 16)
	if err != nil || n == 0 {
		return "", 0, errors.New("tests need a single port number, e.g. tag:server:22")
	}
	return sel, uint16(n), nil
}

// PortRange is an inclusive port range; {0, 65535} means all ports.
type PortRange struct{ First, Last uint16 }

var AllPorts = []PortRange{{0, 65535}}

func ParsePorts(s string) ([]PortRange, error) {
	if s == "*" {
		return AllPorts, nil
	}
	var out []PortRange
	for _, part := range strings.Split(s, ",") {
		part = strings.TrimSpace(part)
		lo, hi, isRange := strings.Cut(part, "-")
		a, err := strconv.ParseUint(lo, 10, 16)
		if err != nil {
			return nil, fmt.Errorf("invalid port %q", part)
		}
		b := a
		if isRange {
			if b, err = strconv.ParseUint(hi, 10, 16); err != nil || b < a {
				return nil, fmt.Errorf("invalid port range %q", part)
			}
		}
		out = append(out, PortRange{uint16(a), uint16(b)})
	}
	return out, nil
}

func parsePrefix(s string) (netip.Prefix, error) {
	if strings.Contains(s, "/") {
		p, err := netip.ParsePrefix(s)
		if err != nil {
			return netip.Prefix{}, err
		}
		return p.Masked(), nil
	}
	a, err := netip.ParseAddr(s)
	if err != nil {
		return netip.Prefix{}, err
	}
	return netip.PrefixFrom(a, a.BitLen()), nil
}
