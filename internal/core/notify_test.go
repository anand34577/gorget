package core

import (
	"context"
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/anand34577/gorget/internal/config"
	"github.com/anand34577/gorget/internal/secrets"
	"github.com/anand34577/gorget/internal/store"
)

func newTestCore(t *testing.T) *Core {
	t.Helper()
	dir := t.TempDir()
	ctx := context.Background()
	cfg := config.Default()
	cfg.PublicURL = "https://vpn.example.com"
	cfg.DataDir = dir
	cfg.TLS.Mode = config.TLSModeOff
	if err := cfg.Finalize(); err != nil {
		t.Fatal(err)
	}
	st, err := store.Open(ctx, "sqlite", filepath.Join(dir, "test.db"), 0)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { st.Close() })
	box, _ := secrets.NewBox(secrets.RandomBytes(32))
	c, err := New(ctx, cfg, st, box, slog.New(slog.NewTextHandler(io.Discard, nil)))
	if err != nil {
		t.Fatal(err)
	}
	return c
}

func TestParseUserAgent(t *testing.T) {
	cases := []struct{ ua, label, key string }{
		{"Mozilla/5.0 (Windows NT 10.0; Win64; x64) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/131.0.0.0 Safari/537.36", "Chrome 131 on Windows", "chrome|windows"},
		{"Mozilla/5.0 (Windows NT 10.0; Win64; x64) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/131.0.0.0 Safari/537.36 Edg/131.0.2903.51", "Edge 131 on Windows", "edge|windows"},
		{"Mozilla/5.0 (X11; Linux x86_64; rv:133.0) Gecko/20100101 Firefox/133.0", "Firefox 133 on Linux", "firefox|linux"},
		{"Mozilla/5.0 (iPhone; CPU iPhone OS 17_5 like Mac OS X) AppleWebKit/605.1.15 (KHTML, like Gecko) Version/17.5 Mobile/15E148 Safari/604.1", "Safari 17 on iOS", "safari|ios"},
		{"Mozilla/5.0 (Linux; Android 14; Pixel 8) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/130.0.0.0 Mobile Safari/537.36", "Chrome 130 on Android", "chrome|android"},
		{"", "Unknown", "unknown"},
	}
	for _, c := range cases {
		label, key := ParseUserAgent(c.ua)
		if label != c.label || key != c.key {
			t.Errorf("ParseUserAgent(%q) = %q, %q; want %q, %q", c.ua, label, key, c.label, c.key)
		}
	}
}

func TestNotificationSettingsAreValidatedAndRedacted(t *testing.T) {
	c := newTestCore(t)
	ctx := context.Background()
	n := defaultNotifications()
	n.Gotify.Enabled, n.Gotify.URL = true, "https://gotify.example.com/"
	if _, err := c.SaveNotificationSettings(ctx, NotificationUpdate{Settings: n}); err == nil {
		t.Fatal("Gotify without a token should be refused")
	}
	tok := "AbCdEf123"
	saved, err := c.SaveNotificationSettings(ctx, NotificationUpdate{Settings: n, GotifyToken: &tok})
	if err != nil {
		t.Fatal(err)
	}
	if saved.Notifications.Gotify.URL != "https://gotify.example.com" {
		t.Errorf("trailing slash kept: %q", saved.Notifications.Gotify.URL)
	}
	red := saved.Redacted().Notifications
	if red.Gotify.TokenSealed != "" || !red.Gotify.TokenSet {
		t.Errorf("token must be hidden but reported as set: %+v", red.Gotify)
	}
	// Saving again without a token keeps the stored one.
	n2 := saved.Notifications
	n2.Gotify.Priority = 7
	if saved, err = c.SaveNotificationSettings(ctx, NotificationUpdate{Settings: n2}); err != nil || saved.Notifications.Gotify.TokenSealed == "" {
		t.Fatalf("token lost on re-save: %v", err)
	}
	// Bad values.
	bad := saved.Notifications
	bad.Login.Mode = "sometimes"
	if _, err := c.SaveNotificationSettings(ctx, NotificationUpdate{Settings: bad}); err == nil {
		t.Error("unknown login mode should be refused")
	}
	bad = saved.Notifications
	bad.Ntfy.Enabled, bad.Ntfy.Topic = true, "has spaces!"
	if _, err := c.SaveNotificationSettings(ctx, NotificationUpdate{Settings: bad}); err == nil {
		t.Error("invalid ntfy topic should be refused")
	}
}

func TestPushDelivery(t *testing.T) {
	c := newTestCore(t)
	ctx := context.Background()
	var mu sync.Mutex
	got := map[string]struct {
		hdr  http.Header
		body map[string]any
	}{}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var body map[string]any
		_ = json.NewDecoder(r.Body).Decode(&body)
		mu.Lock()
		got[r.URL.Path] = struct {
			hdr  http.Header
			body map[string]any
		}{r.Header.Clone(), body}
		mu.Unlock()
		w.WriteHeader(http.StatusOK)
	}))
	defer srv.Close()

	n := defaultNotifications()
	n.Gotify.Enabled, n.Gotify.URL = true, srv.URL+"/gotify"
	n.Ntfy.Enabled, n.Ntfy.URL, n.Ntfy.Topic = true, srv.URL+"/ntfy", "gorget-alerts"
	gt, nt := "gotify-token", "ntfy-token"
	if _, err := c.SaveNotificationSettings(ctx, NotificationUpdate{Settings: n, GotifyToken: &gt, NtfyToken: &nt}); err != nil {
		t.Fatal(err)
	}
	if err := c.SendTestPush(ctx, "gotify"); err != nil {
		t.Fatal(err)
	}
	if err := c.SendTestPush(ctx, "ntfy"); err != nil {
		t.Fatal(err)
	}
	g := got["/gotify/message"]
	if g.hdr.Get("X-Gotify-Key") != "gotify-token" || !strings.HasPrefix(g.body["title"].(string), "Test message") {
		t.Errorf("gotify request: %v %v", g.hdr, g.body)
	}
	nf := got["/ntfy"]
	if nf.hdr.Get("Authorization") != "Bearer ntfy-token" || nf.body["topic"] != "gorget-alerts" {
		t.Errorf("ntfy request: %v %v", nf.hdr, nf.body)
	}

	// An urgent message is raised to a high priority on both services.
	if err := c.deliverPush(ctx, pushMsg{Title: "x", Intro: "y", Urgent: true}, ""); err != nil {
		t.Fatal(err)
	}
	if p := got["/gotify/message"].body["priority"].(float64); p < 8 {
		t.Errorf("gotify priority %v", p)
	}
	if p := got["/ntfy"].body["priority"].(float64); p != 5 {
		t.Errorf("ntfy priority %v", p)
	}
}

func TestPushErrorsAreExplained(t *testing.T) {
	c := newTestCore(t)
	ctx := context.Background()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { w.WriteHeader(http.StatusUnauthorized) }))
	defer srv.Close()
	n := defaultNotifications()
	n.Gotify.Enabled, n.Gotify.URL = true, srv.URL
	tok := "wrong"
	if _, err := c.SaveNotificationSettings(ctx, NotificationUpdate{Settings: n, GotifyToken: &tok}); err != nil {
		t.Fatal(err)
	}
	err := c.SendTestPush(ctx, "gotify")
	if err == nil || !strings.Contains(err.Error(), "refused the token") {
		t.Fatalf("want a helpful token error, got %v", err)
	}
}

func TestLoginHistoryRecognisesNewBrowsersAndCountries(t *testing.T) {
	c := newTestCore(t)
	ctx := context.Background()
	first, nb, nc := c.recordLogin(ctx, "u1", "chrome|windows", "DE")
	if !first || !nb || !nc {
		t.Fatalf("first sign-in: %v %v %v", first, nb, nc)
	}
	first, nb, nc = c.recordLogin(ctx, "u1", "chrome|windows", "DE")
	if first || nb || nc {
		t.Fatalf("same browser and country should be familiar: %v %v %v", first, nb, nc)
	}
	_, nb, nc = c.recordLogin(ctx, "u1", "firefox|linux", "DE")
	if !nb || nc {
		t.Fatalf("new browser, same country: %v %v", nb, nc)
	}
	_, nb, nc = c.recordLogin(ctx, "u1", "firefox|linux", "IN")
	if nb || !nc {
		t.Fatalf("same browser, new country: %v %v", nb, nc)
	}
	// Another account has its own history.
	if first, _, _ = c.recordLogin(ctx, "u2", "chrome|windows", "DE"); !first {
		t.Fatal("a different account starts with no history")
	}
}

func TestWireGuardClientPresence(t *testing.T) {
	c := newTestCore(t) // keepalive 25 s: a client is online for 75 s after it was last heard
	d := &store.Device{ID: "wg1", Kind: store.KindWireGuard}
	if c.wgOnline(d) {
		t.Fatal("never heard from: offline")
	}
	c.NoteWGActivity("wg1", time.Now())
	if !c.wgOnline(d) {
		t.Fatal("just heard from: online")
	}
	c.NoteWGActivity("wg1", time.Now().Add(-2*time.Minute))
	// An older observation never makes a client look older than it is.
	if !c.wgOnline(d) {
		t.Fatal("older activity must not override newer")
	}
	c.wg.mu.Lock()
	c.wg.seen["wg1"] = time.Now().Add(-2 * time.Minute)
	c.wg.mu.Unlock()
	if c.wgOnline(d) {
		t.Fatal("silent for two minutes with a 25 s keepalive: offline")
	}
}

func TestDescribeEvent(t *testing.T) {
	n, ok := describeEvent(Event{Type: EvDevicePending, Data: map[string]string{"id": "d1", "name": "phone", "ipv4": "100.80.0.5"}})
	if !ok || n.kind != "pending" || !strings.Contains(n.subject, "phone") || !n.urgent {
		t.Fatalf("pending: %+v %v", n, ok)
	}
	if _, ok := describeEvent(Event{Type: EvLoginFailed, Data: map[string]any{"locked": "false"}}); ok {
		t.Fatal("a failed login that didn't lock the account is not announced")
	}
	if _, ok := describeEvent(Event{Type: EvDeviceCreated, Data: map[string]string{"state": store.StatePending}}); ok {
		t.Fatal("a pending device is announced as pending, not as added")
	}
	if !(EmailNotify{DeviceOffline: true}).on("offline") || (EmailNotify{}).on("offline") {
		t.Fatal("toggle lookup")
	}
}
