// Command gorget is the Gorget desktop client: the background daemon (run as a
// system service) and the command-line tool that controls it.
package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"os"
	"os/signal"
	"runtime"
	"strings"
	"syscall"

	"github.com/kardianos/service"

	"github.com/anand34577/gorget/client"
	"github.com/anand34577/gorget/client/localapi"
)

const usage = `gorget — Gorget VPN client

Usage:
  gorget <command> [flags]

Connecting:
  up                 Connect (signs in first when needed)
                       -server URL  -setup-key KEY  -exit-node NAME  -allow-lan  -accept-routes
                       -use-dns  -kill-switch  -advertise-exit-node  -advertise-routes CIDR,CIDR
  down               Disconnect (stay signed in)
  login              Sign in without connecting:  -server URL  [-setup-key KEY]
  logout             Sign out and forget this device's keys
  status             Show connection and devices        [-json] [-watch]
  exit-node          List exit nodes;  exit-node set NAME;  exit-node none
  set                Change settings:  -allow-lan=BOOL -accept-routes=BOOL -use-dns=BOOL -post-quantum=BOOL
                       -kill-switch=BOOL -advertise-exit-node=BOOL -advertise-routes=CIDR,CIDR
  netcheck           Show how this device reaches the network (direct, relay, endpoints)
  run                run -bypass -- COMMAND...   start a program outside the tunnel (Linux), e.g. while using an exit node
  file               Send files to your other devices:  file cp FILE... DEVICE | list | get [NAME] [-dir DIR] | rm NAME
  ssh                ssh [USER@]DEVICE [ssh args]   (uses the system ssh, host keys tied to the device name)

Service and access:
  install-service    Install and start the background service (needs root/Administrator)
  uninstall-service  Stop and remove the service
  operator           Show or set who may control the service without root:
                       operator | operator set USER | operator clear
  daemon             Run the daemon in the foreground (the service runs this)
  version            Print the version

Global flag: -socket PATH  use another daemon address.
`

func main() {
	args := os.Args[1:]
	socket := ""
	for i := 0; i < len(args); i++ {
		if args[i] == "-socket" || args[i] == "--socket" {
			if i+1 >= len(args) {
				fatal(errors.New("-socket needs a value"))
			}
			socket = args[i+1]
			args = append(args[:i], args[i+2:]...)
			break
		}
	}
	if len(args) == 0 {
		fmt.Print(usage)
		return
	}
	cmd, rest := args[0], args[1:]
	api := localapi.NewClient(socket)
	var err error
	switch cmd {
	case "daemon":
		err = cmdDaemon(rest, socket)
	case "up":
		err = cmdUp(api, rest)
	case "down":
		err = api.Down(ctx())
	case "login":
		err = cmdLogin(api, rest)
	case "logout":
		err = api.Logout(ctx())
	case "status":
		err = cmdStatus(api, rest)
	case "exit-node":
		err = cmdExitNode(api, rest)
	case "set":
		err = cmdSet(api, rest)
	case "netcheck":
		err = cmdNetcheck(api)
	case "run":
		err = cmdRun(api, rest)
	case "file":
		err = cmdFile(api, rest)
	case "ssh":
		err = cmdSSH(api, rest)
	case "operator":
		err = cmdOperator(api, rest)
	case "install-service", "uninstall-service", "start-service", "stop-service":
		err = cmdService(strings.TrimSuffix(cmd, "-service"), socket)
	case "version", "-v", "--version":
		fmt.Printf("gorget %s (%s/%s, %s)\n", client.Version, runtime.GOOS, runtime.GOARCH, runtime.Version())
	case "help", "-h", "--help":
		fmt.Print(usage)
	default:
		fmt.Fprintf(os.Stderr, "unknown command %q\n\n%s", cmd, usage)
		os.Exit(2)
	}
	if err != nil {
		fatal(err)
	}
}

func fatal(err error) {
	fmt.Fprintln(os.Stderr, "error:", err)
	os.Exit(1)
}

// ctx is a plain context: the daemon bounds its own slow operations.
func ctx() context.Context { return context.Background() }

// ---------- service ----------

func serviceConfig(socket string) *service.Config {
	args := []string{"daemon"}
	if socket != "" {
		args = append(args, "-socket", socket)
	}
	return &service.Config{
		Name:        "gorget",
		DisplayName: "Gorget VPN",
		Description: "Gorget mesh VPN client service.",
		Arguments:   args,
		Option: service.KeyValue{
			"Restart":          "always",
			"OnFailure":        "restart",
			"DelayedAutoStart": false,
			"KeepAlive":        true,
			"RunAtLoad":        true,
		},
	}
}

func cmdService(action, socket string) error {
	s, err := service.New(&daemon{}, serviceConfig(socket))
	if err != nil {
		return err
	}
	if err := service.Control(s, action); err != nil {
		return err
	}
	fmt.Printf("service %s: ok\n", action)
	if action == "install" {
		// Start right away so the first `gorget up` works.
		if err := service.Control(s, "start"); err != nil {
			fmt.Fprintln(os.Stderr, "installed, but it didn't start:", err)
		} else {
			fmt.Println("service started")
		}
	}
	return nil
}

func cmdDaemon(args []string, socket string) error {
	fs := flag.NewFlagSet("daemon", flag.ExitOnError)
	fs.StringVar(&socket, "socket", socket, "local API address")
	dataDir := fs.String("data-dir", localapi.DefaultDataDir(), "state directory")
	debug := fs.Bool("debug", false, "verbose logging")
	_ = fs.Parse(args)
	d := &daemon{socket: socket, dataDir: *dataDir, debug: *debug}
	if service.Interactive() {
		ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
		defer stop()
		return d.run(ctx)
	}
	s, err := service.New(d, serviceConfig(socket))
	if err != nil {
		return err
	}
	return s.Run()
}
