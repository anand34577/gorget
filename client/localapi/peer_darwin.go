//go:build darwin

package localapi

import (
	"net"

	"golang.org/x/sys/unix"
)

// peerUID returns the user id of the process on the other end of a Unix socket.
func peerUID(conn net.Conn) (uint32, bool) {
	uc, ok := conn.(*net.UnixConn)
	if !ok {
		return 0, false
	}
	raw, err := uc.SyscallConn()
	if err != nil {
		return 0, false
	}
	var uid uint32
	var gerr error
	if err := raw.Control(func(fd uintptr) {
		var cred *unix.Xucred
		cred, gerr = unix.GetsockoptXucred(int(fd), unix.SOL_LOCAL, unix.LOCAL_PEERCRED)
		if gerr == nil {
			uid = cred.Uid
		}
	}); err != nil || gerr != nil {
		return 0, false
	}
	return uid, true
}
