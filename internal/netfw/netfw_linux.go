//go:build linux

// Package netfw keeps forwarded traffic of a tunnel interface from being dropped by
// firewalls that were set up before us. Docker and ufw both leave the legacy FORWARD
// chain on "policy DROP"; our own nftables table accepts the traffic, but a drop in any
// other chain at the same hook still wins, so exit-node and gateway traffic would be
// sent and never answered.
package netfw

import (
	"os/exec"
)

// Allow lets traffic in and out of iface through the legacy FORWARD chain. It does
// nothing when iptables isn't installed. Our own rules still decide what is allowed.
func Allow(iface string) {
	for _, bin := range []string{"iptables", "ip6tables"} {
		path, err := exec.LookPath(bin)
		if err != nil {
			continue
		}
		ensure(path, "-i", iface, "-j", "ACCEPT")
		ensure(path, "-o", iface, "-m", "conntrack", "--ctstate", "RELATED,ESTABLISHED", "-j", "ACCEPT")
	}
}

// Revoke removes what Allow added.
func Revoke(iface string) {
	for _, bin := range []string{"iptables", "ip6tables"} {
		path, err := exec.LookPath(bin)
		if err != nil {
			continue
		}
		for _, spec := range [][]string{
			{"-i", iface, "-j", "ACCEPT"},
			{"-o", iface, "-m", "conntrack", "--ctstate", "RELATED,ESTABLISHED", "-j", "ACCEPT"},
		} {
			for exec.Command(path, append([]string{"-C", "FORWARD"}, spec...)...).Run() == nil {
				if exec.Command(path, append([]string{"-D", "FORWARD"}, spec...)...).Run() != nil {
					break
				}
			}
		}
	}
}

func ensure(path string, spec ...string) {
	if exec.Command(path, append([]string{"-C", "FORWARD"}, spec...)...).Run() == nil {
		return
	}
	_ = exec.Command(path, append([]string{"-I", "FORWARD", "1"}, spec...)...).Run()
}
