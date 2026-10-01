package policy

import (
	"net/netip"
	"testing"
)

func pfx(s string) netip.Prefix { return netip.MustParsePrefix(s) }
func addr(s string) netip.Addr  { return netip.MustParseAddr(s) }

func testEnv() Env {
	return Env{
		Groups:  map[string][]string{"eng": {"u-alice"}},
		Overlay: []netip.Prefix{pfx("100.80.0.0/16"), pfx("fd00:1::/48")},
		Nodes: []Node{
			{ID: "alice-laptop", Name: "alice-laptop", OwnerID: "u-alice", OwnerEmail: "alice@example.com", Addrs: []netip.Prefix{pfx("100.80.0.2/32")}},
			{ID: "bob-phone", Name: "bob-phone", OwnerID: "u-bob", OwnerEmail: "bob@example.com", Addrs: []netip.Prefix{pfx("100.80.0.3/32")}},
			{ID: "bob-laptop", Name: "bob-laptop", OwnerID: "u-bob", OwnerEmail: "bob@example.com", Addrs: []netip.Prefix{pfx("100.80.0.6/32")}},
			{ID: "server", Name: "server", OwnerID: "u-alice", Tags: []string{"tag:server"}, Addrs: []netip.Prefix{pfx("100.80.0.4/32")}},
			{ID: "router", Name: "router", Tags: []string{"tag:router"}, Addrs: []netip.Prefix{pfx("100.80.0.5/32")}, Routes: []netip.Prefix{pfx("192.168.1.0/24")}, ExitNode: true},
		},
	}
}

const testPolicy = `{
  // comment allowed
  "hosts": { "nas": "192.168.1.10" },
  "tagOwners": { "tag:server": ["group:eng"] },
  "acls": [
    { "id": "eng-ssh", "action": "accept", "src": ["group:eng"], "dst": ["tag:server:22", "nas:445"] },
    { "id": "self", "action": "accept", "src": ["autogroup:member"], "dst": ["autogroup:self:*"] },
    { "id": "inet", "action": "accept", "src": ["bob@example.com"], "dst": ["autogroup:internet:*"] },
  ],
  "tests": [
    { "src": "alice@example.com", "accept": ["tag:server:22", "nas:445"], "deny": ["tag:server:80", "nas:22", "device:bob-phone:22"] },
    { "src": "bob@example.com", "accept": ["device:bob-laptop:22"], "deny": ["tag:server:22", "device:alice-laptop:22"] }
  ]
}`

func TestCompileAndCheck(t *testing.T) {
	p, err := Parse(testPolicy)
	if err != nil {
		t.Fatal(err)
	}
	env := testEnv()
	c := p.Compile(env)

	cases := []struct {
		src, dst string
		port     uint16
		want     bool
	}{
		{"100.80.0.2", "100.80.0.4", 22, true},     // alice -> server ssh
		{"100.80.0.2", "100.80.0.4", 80, false},    // wrong port
		{"100.80.0.2", "192.168.1.10", 445, true},  // nas via router
		{"100.80.0.2", "192.168.1.10", 22, false},  // ports don't leak between dst entries
		{"100.80.0.3", "100.80.0.4", 22, false},    // bob not in eng
		{"100.80.0.3", "100.80.0.6", 22, true},     // bob self
		{"100.80.0.3", "100.80.0.2", 22, false},    // bob -> alice denied
		{"100.80.0.3", "8.8.8.8", 443, true},       // bob internet
		{"100.80.0.3", "192.168.1.20", 443, false}, // internet rule must not cover private routes
		{"100.80.0.3", "100.80.0.5", 22, false},    // nor overlay
		{"100.80.0.2", "8.8.8.8", 443, false},      // alice has no internet
	}
	for _, tc := range cases {
		got := c.Check(addr(tc.src), addr(tc.dst), tc.port, "tcp")
		if got.Allowed != tc.want {
			t.Errorf("%s -> %s:%d = %v (%s), want %v", tc.src, tc.dst, tc.port, got.Allowed, got.Reason, tc.want)
		}
	}

	if !c.IsPeer("alice-laptop", "server") || !c.IsPeer("alice-laptop", "router") {
		t.Error("alice should peer with server and router")
	}
	if c.IsPeer("bob-phone", "server") {
		t.Error("bob should not peer with server")
	}
	if !c.IsPeer("bob-phone", "router") {
		t.Error("bob should peer with exit node router")
	}
	if !c.CanUseExitNode("bob-phone") || c.CanUseExitNode("alice-laptop") {
		t.Error("exit node permissions wrong")
	}
	in := c.InboundRules("server")
	if len(in) != 1 || in[0].Ports[0].First != 22 {
		t.Errorf("server inbound = %+v", in)
	}
	for _, r := range c.RunTests(env) {
		if !r.Passed {
			t.Errorf("test failed: %+v", r)
		}
	}
}

func TestValidate(t *testing.T) {
	bad := `{"acls":[{"action":"deny","src":["autogroup:internet"],"dst":["tag:x"]}]}`
	if _, err := Parse(bad); err == nil {
		t.Fatal("expected validation error")
	} else if ve, ok := err.(*ValidationError); !ok || len(ve.Problems) < 3 {
		t.Fatalf("got %v", err)
	}
	if _, err := Parse(DefaultAllowAll); err != nil {
		t.Fatal(err)
	}
	if _, err := Parse(DefaultDeny); err != nil {
		t.Fatal(err)
	}
}

func TestAutoApprove(t *testing.T) {
	p, _ := Parse(`{"acls":[],"autoApprovers":{"routes":{"192.168.0.0/16":["tag:router"]},"exitNode":["tag:router"]}}`)
	env := testEnv()
	router := env.Nodes[4]
	if !p.AutoApproveRoute(env, router, pfx("192.168.1.0/24")) {
		t.Error("route should auto-approve")
	}
	if p.AutoApproveRoute(env, router, pfx("10.0.0.0/8")) {
		t.Error("route outside should not")
	}
	if !p.AutoApproveExitNode(env, router) || p.AutoApproveExitNode(env, env.Nodes[0]) {
		t.Error("exit auto-approve wrong")
	}
}
