package localapi

import (
	"context"
	"fmt"
	"os"
	"os/user"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"

	"github.com/anand34577/gorget/client"
)

func TestLocalAPI(t *testing.T) {
	skipServerCheck = true // the test server is not a privileged service
	dir := t.TempDir()
	c, err := client.New(client.Options{DataDir: dir})
	if err != nil {
		t.Fatal(err)
	}
	addr := filepath.Join(dir, "t.sock")
	if runtime.GOOS == "windows" {
		addr = fmt.Sprintf(`\\.\pipe\gorget-test-%d`, time.Now().UnixNano())
	}
	ln, err := Listen(addr)
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	srv := New(c, dir)
	go srv.Serve(ctx, ln)
	api := NewClient(addr)

	// Reads are open to everyone.
	st, err := api.Status(ctx)
	if err != nil || st.State != client.StateNoServer {
		t.Fatalf("status = %+v, %v", st, err)
	}
	who, err := api.WhoAmI(ctx)
	if err != nil {
		t.Fatal(err)
	}

	// Writes need admin or the operator.
	if !who.Admin {
		err := api.SetServer(ctx, "vpn.example.com")
		if err == nil || !strings.Contains(err.Error(), "access denied") {
			t.Fatalf("an ordinary user must not change settings, got %v", err)
		}
		u, err := user.Current()
		if err != nil {
			t.Fatal(err)
		}
		if err := SetOperator(dir, u.Username); err != nil {
			t.Fatal(err)
		}
	}
	p, err := api.SetPrefs(ctx, map[string]any{"kill_switch": true, "advertise_routes": []string{"192.168.1.0/24"}})
	if err != nil || !p.KillSwitch || len(p.AdvertiseRoutes) != 1 || !p.AllowLAN {
		t.Fatalf("prefs = %+v, %v (a patch must keep fields it doesn't mention)", p, err)
	}
	if _, err := api.SetPrefs(ctx, map[string]any{"advertise_routes": []string{"not-a-cidr"}}); err == nil {
		t.Fatal("invalid routes must be rejected")
	}
	if _, err := api.SetPrefs(ctx, map[string]any{"bogus": 1}); err == nil {
		t.Fatal("unknown fields must be rejected")
	}
	if err := api.Up(ctx); err == nil {
		t.Fatal("up before sign-in must fail")
	}

	// The event stream delivers the current state first, then changes.
	got := make(chan client.Status, 4)
	wctx, wcancel := context.WithCancel(ctx)
	defer wcancel()
	go api.Watch(wctx, func(s client.Status) { got <- s })
	select {
	case s := <-got:
		if s.State != client.StateNoServer {
			t.Fatalf("first event = %s", s.State)
		}
	case <-time.After(3 * time.Second):
		t.Fatal("no event")
	}

	if err := SetOperator(dir, ""); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(dir, "operator")); !os.IsNotExist(err) {
		t.Fatal("operator file should be gone")
	}
}

func TestOperatorMatches(t *testing.T) {
	host, _ := os.Hostname()
	cases := []struct {
		op, user string
		want     bool
	}{
		{"anand", "anand", true},
		{"anand", "bob", false},
		{"", "anand", false},
		{"anand", "", false},
		{`PC\anand`, `PC\anand`, true},
		{`PC\anand`, `OTHER\anand`, false},
		{"anand", `EVIL\anand`, false},
		{"anand", shortHostname(host) + `\anand`, true},
	}
	for _, c := range cases {
		if got := operatorMatches(c.op, c.user); got != c.want {
			t.Errorf("operatorMatches(%q, %q) = %v, want %v", c.op, c.user, got, c.want)
		}
	}
}
