// Package osrouter configures the operating system for the Gorget client: it
// creates the TUN interface, installs addresses, routes and DNS, keeps the
// daemon's own sockets outside the tunnel, and applies the kill switch and
// forwarding/NAT when the device serves as a subnet router or exit node.
//
// Each OS has its own implementation; all satisfy client.Platform.
package osrouter

import (
	"log/slog"
	"regexp"
	"strings"

	"github.com/anand34577/gorget/client"
)

// InterfaceName is the preferred TUN name (Windows adapter name, Linux link name).
const InterfaceName = "gorget0"

// Router is a client.Platform for desktop operating systems.
type Router interface {
	client.Platform
	// Close removes everything the router configured.
	Close() error
}

// AppBypasser is implemented by routers that can keep chosen programs outside the tunnel
// (split tunnelling by application). Only Linux can do this without a kernel driver.
type AppBypasser interface {
	// BypassApp moves a process (and everything it starts later) outside the tunnel.
	BypassApp(pid int) error
}

// New returns the router for the running OS.
func New(log *slog.Logger) (Router, error) { return newRouter(log) }

func trimDot(d string) string { return strings.TrimSuffix(strings.TrimSpace(d), ".") }

// validDomain accepts only plain DNS names. Domains come from the server and end up
// in file paths (/etc/resolver), config files and commands run as root/SYSTEM, so a
// name with slashes, quotes, spaces or newlines is dropped.
var validDomain = regexp.MustCompile(`^[A-Za-z0-9_]([A-Za-z0-9_-]{0,62}\.)*[A-Za-z0-9_-]{1,63}$`)

// cleanDomains trims, validates and de-duplicates server-supplied domain names.
func cleanDomains(in []string) []string {
	seen := map[string]bool{}
	var out []string
	for _, d := range in {
		d = strings.ToLower(trimDot(d))
		if len(d) > 253 || !validDomain.MatchString(d) || seen[d] {
			continue
		}
		seen[d] = true
		out = append(out, d)
	}
	return out
}
