package core

import (
	"strings"
	"testing"

	"github.com/anand34577/gorget/internal/store"
)

func TestCompareVersions(t *testing.T) {
	cases := []struct {
		a, b string
		want int
	}{
		{"0.3.0", "0.3.0", 0}, {"v0.3.1", "0.3.0", 1}, {"0.2.9", "0.3.0", -1}, {"1.0", "1.0.0", 0},
		{"0.3.0-dirty", "0.3.0", 0}, {"10.0.19045", "10.0.9", 1}, {"dev", "0.1.0", -1},
	}
	for _, c := range cases {
		if got := compareVersions(c.a, c.b); got != c.want {
			t.Errorf("compare(%q,%q)=%d want %d", c.a, c.b, got, c.want)
		}
	}
}

func TestExtractVersion(t *testing.T) {
	for in, want := range map[string]string{
		"Windows 10.0 (build 26200)": "10.0", "macOS 14.5": "14.5", "Ubuntu 24.04.1 LTS": "24.04.1", "": "", "Arch Linux": "",
	} {
		if got := extractVersion(in); got != want {
			t.Errorf("extractVersion(%q)=%q want %q", in, got, want)
		}
	}
}

func TestPostureFailures(t *testing.T) {
	p := PostureSettings{Enabled: true, Mode: PostureEnforce, MinClientVersion: "0.3.0",
		MinOSVersion: map[string]string{"darwin": "13"}, RequireDiskEncryption: true, RequireFirewall: true,
		AllowedNetworks: []string{"203.0.113.0/24"}, ExemptTags: []string{"tag:server"}}
	ok := &store.Device{Kind: store.KindNative, OS: "darwin", OSVersion: "macOS 14.2", ClientVersion: "0.3.1", DiskEncrypted: 1, FirewallOn: 1, PublicIP: "203.0.113.9"}
	if f := PostureFailures(p, ok, ""); len(f) != 0 {
		t.Fatalf("compliant device failed: %v", f)
	}
	bad := *ok
	bad.OSVersion, bad.ClientVersion, bad.DiskEncrypted, bad.FirewallOn, bad.PublicIP = "macOS 12.6", "0.2.0", 2, 0, "198.51.100.1"
	if f := PostureFailures(p, &bad, ""); len(f) != 5 {
		t.Fatalf("want 5 failures, got %v", f)
	}
	exempt := bad
	exempt.Tags = store.StringList{"tag:server"}
	if f := PostureFailures(p, &exempt, ""); len(f) != 0 {
		t.Fatalf("exempt device failed: %v", f)
	}
	// Unknown encryption status (old client) must not pass a "required" rule.
	unk := *ok
	unk.DiskEncrypted = 0
	if f := PostureFailures(p, &unk, ""); len(f) != 1 || !strings.Contains(f[0], "unknown") {
		t.Fatalf("unknown encryption: %v", f)
	}
	p.Enabled = false
	if f := PostureFailures(p, &bad, ""); f != nil {
		t.Fatal("disabled posture must not fail anyone")
	}
	// The gateway and WireGuard devices are never checked.
	gw := bad
	gw.Kind = store.KindGateway
	p.Enabled = true
	if f := PostureFailures(p, &gw, ""); f != nil {
		t.Fatal("gateway must be exempt")
	}
}

func TestValidatePosture(t *testing.T) {
	if validatePosture(PostureSettings{Mode: "bogus"}) == nil {
		t.Fatal("bad mode accepted")
	}
	if validatePosture(PostureSettings{Mode: PostureReport, MinClientVersion: "abc"}) == nil {
		t.Fatal("bad version accepted")
	}
	if validatePosture(PostureSettings{Mode: PostureReport, MinOSVersion: map[string]string{"beos": "1"}}) == nil {
		t.Fatal("bad os accepted")
	}
	if validatePosture(PostureSettings{Mode: PostureReport, AllowedNetworks: []string{"nope"}}) == nil {
		t.Fatal("bad network accepted")
	}
	if err := validatePosture(PostureSettings{Mode: PostureEnforce, MinOSVersion: map[string]string{"windows": "10.0.19045"}, AllowedNetworks: []string{"1.2.3.4", "10.0.0.0/8"}}); err != nil {
		t.Fatal(err)
	}
}

func TestPostureCountriesAndOS(t *testing.T) {
	p := PostureSettings{Enabled: true, Mode: PostureEnforce, AllowedCountries: []string{"DE", "IN"}, AllowedOS: []string{"linux", "android"}}
	d := &store.Device{Kind: store.KindNative, OS: "linux", PublicIP: "203.0.113.9"}
	if f := PostureFailures(p, d, "IN"); len(f) != 0 {
		t.Fatalf("allowed country and OS: %v", f)
	}
	if f := PostureFailures(p, d, "US"); len(f) != 1 {
		t.Fatalf("other country must fail: %v", f)
	}
	if f := PostureFailures(p, d, ""); len(f) != 1 {
		t.Fatalf("unknown country must fail when countries are limited: %v", f)
	}
	win := *d
	win.OS = "windows"
	if f := PostureFailures(p, &win, "DE"); len(f) != 1 {
		t.Fatalf("windows isn't allowed: %v", f)
	}
	b := PostureSettings{Enabled: true, Mode: PostureEnforce, BlockedCountries: []string{"KP"}}
	if f := PostureFailures(b, d, ""); len(f) != 0 {
		t.Fatalf("unknown country passes a block list: %v", f)
	}
	if f := PostureFailures(b, d, "KP"); len(f) != 1 {
		t.Fatalf("blocked country: %v", f)
	}
	bad := PostureSettings{Mode: PostureEnforce, AllowedCountries: []string{"Germany"}}
	if validatePosture(bad) == nil {
		t.Fatal("country names must be rejected")
	}
}
