//go:build windows

package localapi

import (
	"context"
	"net"
	"os"
	"path/filepath"
	"time"

	"github.com/Microsoft/go-winio"
	"golang.org/x/sys/windows"
)

// DefaultAddr is the daemon's named pipe.
func DefaultAddr() string { return `\\.\pipe\gorget` }

// DefaultDataDir is where the daemon keeps its state.
func DefaultDataDir() string {
	if pd := os.Getenv("ProgramData"); pd != "" {
		return filepath.Join(pd, "Gorget")
	}
	return `C:\ProgramData\Gorget`
}

// Listen creates the pipe: SYSTEM and Administrators full control, every signed-in
// user may connect (the server decides per request who may change things).
func Listen(addr string) (net.Listener, error) {
	return winio.ListenPipe(addr, &winio.PipeConfig{
		SecurityDescriptor: "D:P(A;;GA;;;SY)(A;;GA;;;BA)(A;;GRGW;;;AU)",
		MessageMode:        false,
		InputBufferSize:    64 << 10,
		OutputBufferSize:   64 << 10,
	})
}

func dial(ctx context.Context, addr string) (net.Conn, error) {
	if _, ok := ctx.Deadline(); !ok {
		var cancel context.CancelFunc
		ctx, cancel = context.WithTimeout(ctx, 5*time.Second)
		defer cancel()
	}
	timeout := 5 * time.Second
	if d, ok := ctx.Deadline(); ok {
		timeout = time.Until(d)
	}
	conn, err := winio.DialPipe(addr, &timeout)
	if err != nil {
		return nil, err
	}
	// Any user can create a pipe with this name while the service isn't running.
	// Only talk to one served by SYSTEM or an elevated administrator, so a setup key
	// or sign-in never goes to an impostor.
	if !skipServerCheck && !serverIsTrusted(conn) {
		conn.Close()
		return nil, ErrUntrustedDaemon
	}
	return conn, nil
}

func serverIsTrusted(conn net.Conn) bool {
	f, ok := conn.(interface{ Fd() uintptr })
	if !ok {
		return false
	}
	var pid uint32
	if windows.GetNamedPipeServerProcessId(windows.Handle(f.Fd()), &pid) != nil {
		return false
	}
	return processIsAdmin(pid)
}

// processIsAdmin reports whether a process runs as SYSTEM or with an elevated
// Administrators token.
func processIsAdmin(pid uint32) bool {
	proc, err := windows.OpenProcess(windows.PROCESS_QUERY_LIMITED_INFORMATION, false, pid)
	if err != nil {
		return false
	}
	defer windows.CloseHandle(proc)
	var tok windows.Token
	if windows.OpenProcessToken(proc, windows.TOKEN_QUERY, &tok) != nil {
		return false
	}
	defer tok.Close()
	if u, err := tok.GetTokenUser(); err == nil && u.User.Sid.IsWellKnown(windows.WinLocalSystemSid) {
		return true
	}
	return tok.IsElevated()
}

// callerOf identifies the process at the other end of the pipe.
func callerOf(conn net.Conn) Caller {
	f, ok := conn.(interface{ Fd() uintptr })
	if !ok {
		return Caller{}
	}
	var pid uint32
	if windows.GetNamedPipeClientProcessId(windows.Handle(f.Fd()), &pid) != nil {
		return Caller{}
	}
	proc, err := windows.OpenProcess(windows.PROCESS_QUERY_LIMITED_INFORMATION, false, pid)
	if err != nil {
		return Caller{}
	}
	defer windows.CloseHandle(proc)
	var tok windows.Token
	if windows.OpenProcessToken(proc, windows.TOKEN_QUERY, &tok) != nil {
		return Caller{}
	}
	defer tok.Close()
	var c Caller
	if u, err := tok.GetTokenUser(); err == nil {
		if u.User.Sid.IsWellKnown(windows.WinLocalSystemSid) {
			c.Admin = true
		}
		if name, domain, _, err := u.User.Sid.LookupAccount(""); err == nil {
			c.User = domain + `\` + name
		}
	}
	// IsMember is false for the deny-only Administrators group of an unelevated token,
	// so an unelevated administrator is treated as a normal user (use the operator).
	if admins, err := windows.CreateWellKnownSid(windows.WinBuiltinAdministratorsSid); err == nil {
		if isMember, err := tok.IsMember(admins); err == nil && isMember {
			c.Admin = true
		}
	}
	return c
}
