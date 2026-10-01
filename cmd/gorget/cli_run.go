package main

import (
	"errors"
	"flag"
	"os"
	"os/exec"
	"runtime"

	"github.com/anand34577/gorget/client/localapi"
)

// cmdRun starts a program outside the tunnel (per-app routing, Linux only).
func cmdRun(api *localapi.Client, args []string) error {
	fs := flag.NewFlagSet("run", flag.ExitOnError)
	bypass := fs.Bool("bypass", true, "keep this program's traffic outside the tunnel")
	_ = fs.Parse(args)
	cmdline := fs.Args()
	if len(cmdline) == 0 {
		return errors.New("usage: gorget run -bypass -- COMMAND [ARGS]")
	}
	if runtime.GOOS != "linux" {
		return errors.New("per-app routing is only available on Linux (Windows needs a signed kernel driver, macOS a network extension)")
	}
	if !*bypass {
		return errors.New("only -bypass is supported: programs already use the tunnel by default")
	}
	// The child waits on a pipe until the daemon has moved it out of the tunnel, so it
	// can't open a connection first. fd 3 is the read end.
	pr, pw, err := os.Pipe()
	if err != nil {
		return err
	}
	defer pw.Close()
	child := exec.Command("/bin/sh", append([]string{"-c", `read -r _ <&3; exec "$@"`, "gorget-run"}, cmdline...)...)
	child.Stdin, child.Stdout, child.Stderr = os.Stdin, os.Stdout, os.Stderr
	child.ExtraFiles = []*os.File{pr}
	if err := child.Start(); err != nil {
		return err
	}
	pr.Close()
	if err := api.BypassApp(ctx(), child.Process.Pid); err != nil {
		_ = child.Process.Kill()
		_ = child.Wait()
		return err
	}
	_, _ = pw.Write([]byte("\n")) // release the child
	pw.Close()
	if err := child.Wait(); err != nil {
		var ee *exec.ExitError
		if errors.As(err, &ee) {
			os.Exit(ee.ExitCode())
		}
		return err
	}
	return nil
}
