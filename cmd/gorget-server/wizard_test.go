package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/anand34577/gorget/internal/config"
)

func TestSetupLink(t *testing.T) {
	dir := t.TempDir()
	cfg := config.Config{PublicURL: "https://vpn.example.com/", DataDir: dir}
	if got := setupLink(cfg); got != "" {
		t.Fatalf("no token file: want empty link, got %q", got)
	}
	if err := os.WriteFile(filepath.Join(dir, "setup-token"), []byte("abc123\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if got, want := setupLink(cfg), "https://vpn.example.com/setup#token=abc123"; got != want {
		t.Fatalf("got %q, want %q", got, want)
	}
}

func TestInitScriptedRejectsBadFlags(t *testing.T) {
	cases := []struct {
		name string
		args []string
		want string
	}{
		{"no domain", []string{"-yes"}, "-domain"},
		{"acme needs a domain name", []string{"-domain", "203.0.113.5", "-tls", "acme"}, "domain name"},
		{"unknown tls", []string{"-domain", "vpn.example.com", "-tls", "bogus"}, "-tls"},
		{"bad database url", []string{"-domain", "vpn.example.com", "-database-url", "mysql://x"}, "-database-url"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			args := append([]string{"-config", filepath.Join(t.TempDir(), "config.yaml")}, c.args...)
			err := cmdInit(args)
			if err == nil || !strings.Contains(err.Error(), c.want) {
				t.Fatalf("want an error mentioning %q, got %v", c.want, err)
			}
		})
	}
}
