//go:build !windows && !linux && !darwin

package localapi

import "net"

// peerUID: no peer credentials on this OS, so every caller is read-only.
func peerUID(net.Conn) (uint32, bool) { return 0, false }
