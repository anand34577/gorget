// Package posture detects the device health facts the server's posture rules
// can ask for: whether the disk is encrypted and whether the firewall is on.
// Values are 0 = unknown, 1 = yes, 2 = no (the same encoding as the protocol's Tri).
package posture

import (
	"context"
	"os/exec"
	"strings"
	"sync"
	"time"
)

const (
	Unknown = 0
	Yes     = 1
	No      = 2
)

type State struct {
	DiskEncrypted int
	Firewall      int
}

var (
	mu      sync.Mutex
	cached  State
	at      time.Time
	running bool
	ready   = make(chan struct{}) // closed after the first detection finishes
	once    sync.Once
)

// Detect returns the posture. Detection runs in the background (the checks start external
// tools and can take seconds); the first call waits up to three seconds for a result and
// later calls return the latest one at once. Results are refreshed every ten minutes.
// Undetectable facts are Unknown.
func Detect() State {
	mu.Lock()
	if !running && (at.IsZero() || time.Since(at) > 10*time.Minute) {
		running = true
		go refresh()
	}
	mu.Unlock()
	select {
	case <-ready:
	case <-time.After(3 * time.Second):
	}
	mu.Lock()
	defer mu.Unlock()
	return cached
}

func refresh() {
	st := State{DiskEncrypted: detectDisk(), Firewall: detectFirewall()}
	mu.Lock()
	cached, at, running = st, time.Now(), false
	mu.Unlock()
	once.Do(func() { close(ready) })
}

// run executes a command with a short timeout and returns its output ("" on error).
func run(name string, args ...string) string {
	ctx, cancel := context.WithTimeout(context.Background(), 8*time.Second)
	defer cancel()
	out, _ := exec.CommandContext(ctx, name, args...).CombinedOutput()
	return string(out)
}

// parseBitLocker reads `manage-bde -status C:` output.
func parseBitLocker(out string) int {
	l := strings.ToLower(out)
	switch {
	case strings.Contains(l, "protection on"):
		return Yes
	case strings.Contains(l, "protection off"), strings.Contains(l, "fully decrypted"):
		return No
	}
	return Unknown
}

// parseNetshFirewall reads `netsh advfirewall show allprofiles state`: every profile must be ON.
func parseNetshFirewall(out string) int {
	on, off := 0, 0
	for _, line := range strings.Split(strings.ToLower(out), "\n") {
		f := strings.Fields(line)
		if len(f) == 2 && f[0] == "state" {
			if f[1] == "on" {
				on++
			} else if f[1] == "off" {
				off++
			}
		}
	}
	switch {
	case off > 0:
		return No
	case on > 0:
		return Yes
	}
	return Unknown
}

// parseFileVault reads `fdesetup status`.
func parseFileVault(out string) int {
	l := strings.ToLower(out)
	switch {
	case strings.Contains(l, "filevault is on"):
		return Yes
	case strings.Contains(l, "filevault is off"):
		return No
	}
	return Unknown
}

// parseSocketFilter reads `socketfilterfw --getglobalstate`.
func parseSocketFilter(out string) int {
	l := strings.ToLower(out)
	switch {
	case strings.Contains(l, "enabled"):
		return Yes
	case strings.Contains(l, "disabled"):
		return No
	}
	return Unknown
}
