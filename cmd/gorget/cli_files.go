package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"net/netip"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"text/tabwriter"

	"github.com/anand34577/gorget/client"
	"github.com/anand34577/gorget/client/filetransfer"
	"github.com/anand34577/gorget/client/localapi"
)

// resolvePeer finds a peer by name, full name or address.
func resolvePeer(st client.Status, q string) (client.PeerView, error) {
	q = strings.TrimSuffix(strings.TrimSpace(q), ".")
	var hit []client.PeerView
	for _, p := range st.Peers {
		if strings.EqualFold(p.Name, q) || strings.EqualFold(p.FQDN, q) || p.IPv4 == q || p.IPv6 == q {
			hit = append(hit, p)
		}
	}
	switch len(hit) {
	case 1:
		return hit[0], nil
	case 0:
		return client.PeerView{}, fmt.Errorf("no device called %q (see: gorget status)", q)
	}
	return client.PeerView{}, fmt.Errorf("%q matches several devices; use the full name", q)
}

func peerAddr(p client.PeerView) (netip.Addr, error) {
	for _, s := range []string{p.IPv4, p.IPv6} {
		if a, err := netip.ParseAddr(s); err == nil {
			return a, nil
		}
	}
	return netip.Addr{}, errors.New("that device has no address")
}

// cmdFile: send files to your other devices and manage the ones you received.
func cmdFile(api *localapi.Client, args []string) error {
	if len(args) == 0 {
		return errors.New("usage: gorget file cp FILE... DEVICE | list | get [NAME...] [-dir DIR] | rm NAME")
	}
	st, err := api.Status(ctx())
	if err != nil {
		return err
	}
	switch args[0] {
	case "cp", "send":
		if len(args) < 3 {
			return errors.New("usage: gorget file cp FILE... DEVICE")
		}
		if st.State != client.StateRunning {
			return errors.New("not connected; run: gorget up")
		}
		p, err := resolvePeer(st, args[len(args)-1])
		if err != nil {
			return err
		}
		to, err := peerAddr(p)
		if err != nil {
			return err
		}
		for _, path := range args[1 : len(args)-1] {
			last := -1
			err := filetransfer.Send(context.Background(), to, path, func(sent, total int64) {
				if total <= 0 {
					return
				}
				if pct := int(sent * 100 / total); pct != last && pct%10 == 0 {
					last = pct
					fmt.Fprintf(os.Stderr, "\r%s: %d%%", filepath.Base(path), pct)
				}
			})
			fmt.Fprint(os.Stderr, "\r")
			if err != nil {
				return fmt.Errorf("%s: %w", path, err)
			}
			fmt.Printf("sent %s to %s\n", filepath.Base(path), p.Name)
		}
		return nil
	case "list", "ls":
		files, err := api.Files(ctx())
		if err != nil {
			return err
		}
		if len(files) == 0 {
			fmt.Println("No received files.")
			return nil
		}
		tw := tabwriter.NewWriter(os.Stdout, 0, 4, 2, ' ', 0)
		for _, f := range files {
			fmt.Fprintf(tw, "%s\t%s\tfrom %s\t%s\n", f.Name, bytesStr(uint64(f.Size)), orElse(f.From, "?"), f.Received.Local().Format("2006-01-02 15:04"))
		}
		return tw.Flush()
	case "get":
		fs := flag.NewFlagSet("file get", flag.ExitOnError)
		dir := fs.String("dir", ".", "where to save the files")
		keep := fs.Bool("keep", false, "keep the files in the inbox after saving")
		_ = fs.Parse(args[1:])
		names := fs.Args()
		if len(names) == 0 {
			files, err := api.Files(ctx())
			if err != nil {
				return err
			}
			for _, f := range files {
				names = append(names, f.Name)
			}
		}
		for _, name := range names {
			dst := filepath.Join(*dir, filepath.Base(name))
			out, err := os.OpenFile(dst, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0o600)
			if err != nil {
				return err
			}
			err = api.DownloadFile(ctx(), name, out)
			if cerr := out.Close(); err == nil {
				err = cerr
			}
			if err != nil {
				_ = os.Remove(dst)
				return fmt.Errorf("%s: %w", name, err)
			}
			if !*keep {
				_ = api.DeleteFile(ctx(), name)
			}
			fmt.Println("saved", dst)
		}
		return nil
	case "rm":
		if len(args) < 2 {
			return errors.New("usage: gorget file rm NAME")
		}
		return api.DeleteFile(ctx(), args[1])
	}
	return fmt.Errorf("unknown file command %q", args[0])
}

// cmdSSH opens ssh to another device by name, using the system ssh client.
func cmdSSH(api *localapi.Client, args []string) error {
	if len(args) == 0 {
		return errors.New("usage: gorget ssh [USER@]DEVICE [ssh options and command]")
	}
	st, err := api.Status(ctx())
	if err != nil {
		return err
	}
	if st.State != client.StateRunning {
		return errors.New("not connected; run: gorget up")
	}
	target, rest := args[0], args[1:]
	user := ""
	if i := strings.LastIndex(target, "@"); i >= 0 {
		user, target = target[:i+1], target[i+1:]
	}
	p, err := resolvePeer(st, target)
	if err != nil {
		return err
	}
	addr, err := peerAddr(p)
	if err != nil {
		return err
	}
	ssh, err := exec.LookPath("ssh")
	if err != nil {
		return errors.New("the ssh program was not found; install an OpenSSH client")
	}
	// HostKeyAlias keeps host keys tied to the device name, not to its address.
	full := append([]string{"-o", "HostKeyAlias=" + p.Name, user + addr.String()}, rest...)
	cmd := exec.Command(ssh, full...)
	cmd.Stdin, cmd.Stdout, cmd.Stderr = os.Stdin, os.Stdout, os.Stderr
	if err := cmd.Run(); err != nil {
		var ee *exec.ExitError
		if errors.As(err, &ee) {
			os.Exit(ee.ExitCode())
		}
		return err
	}
	return nil
}
