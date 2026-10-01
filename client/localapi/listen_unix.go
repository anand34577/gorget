//go:build !windows

package localapi

import (
	"context"
	"net"
	"os"
	"os/user"
	"path/filepath"
	"runtime"
	"strconv"
	"syscall"
)

// DefaultAddr is the daemon's socket path.
func DefaultAddr() string {
	if runtime.GOOS == "darwin" {
		return "/var/run/gorget.sock"
	}
	return "/var/run/gorget/gorget.sock"
}

// DefaultDataDir is where the daemon keeps its state.
func DefaultDataDir() string {
	if runtime.GOOS == "darwin" {
		return "/Library/Application Support/Gorget"
	}
	return "/var/lib/gorget"
}

// Listen creates the socket. Everyone may connect; the server decides per request
// who may change things (reads are open to all local users).
func Listen(addr string) (net.Listener, error) {
	if err := os.MkdirAll(filepath.Dir(addr), 0o755); err != nil {
		return nil, err
	}
	_ = os.Remove(addr)
	ln, err := net.Listen("unix", addr)
	if err != nil {
		return nil, err
	}
	if err := os.Chmod(addr, 0o666); err != nil {
		ln.Close()
		return nil, err
	}
	return ln, nil
}

func dial(ctx context.Context, addr string) (net.Conn, error) {
	// The socket must belong to root (the service) or to us; anything else could be
	// another user's program waiting for a setup key.
	if fi, err := os.Stat(addr); err == nil && !skipServerCheck {
		if st, ok := fi.Sys().(*syscall.Stat_t); ok && st.Uid != 0 && int(st.Uid) != os.Getuid() {
			return nil, ErrUntrustedDaemon
		}
	}
	var d net.Dialer
	return d.DialContext(ctx, "unix", addr)
}

func callerOf(conn net.Conn) Caller {
	uid, ok := peerUID(conn)
	if !ok {
		return Caller{}
	}
	c := Caller{Admin: uid == 0}
	if u, err := user.LookupId(strconv.FormatUint(uint64(uid), 10)); err == nil {
		c.User = u.Username
	}
	return c
}
