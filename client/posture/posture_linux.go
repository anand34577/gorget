//go:build linux

package posture

import (
	"os"
	"path/filepath"
	"strings"
)

// detectDisk follows the root device down through LVM/RAID to see whether a LUKS (dm-crypt) layer is below it.
func detectDisk() int {
	src := strings.TrimSpace(run("findmnt", "-no", "SOURCE", "/"))
	if src == "" {
		return Unknown
	}
	if i := strings.IndexByte(src, '['); i > 0 { // btrfs subvolume: /dev/mapper/x[/@]
		src = src[:i]
	}
	real, err := filepath.EvalSymlinks(src)
	if err != nil {
		return Unknown
	}
	if cryptBelow(filepath.Base(real), 0) {
		return Yes
	}
	return No
}

func cryptBelow(dev string, depth int) bool {
	if depth > 6 {
		return false
	}
	if b, err := os.ReadFile("/sys/class/block/" + dev + "/dm/uuid"); err == nil && strings.HasPrefix(string(b), "CRYPT-") {
		return true
	}
	slaves, _ := os.ReadDir("/sys/class/block/" + dev + "/slaves")
	for _, s := range slaves {
		if cryptBelow(s.Name(), depth+1) {
			return true
		}
	}
	return false
}

func detectFirewall() int {
	known := false
	for _, unit := range []string{"ufw", "firewalld", "nftables", "iptables"} {
		out := strings.TrimSpace(run("systemctl", "is-active", unit))
		if out == "active" {
			return Yes
		}
		if out == "inactive" || out == "failed" {
			known = true
		}
	}
	if known {
		return No
	}
	return Unknown
}
