package main

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"net/netip"
	"os"
	"os/exec"
	"runtime"
	"strings"
	"text/tabwriter"
	"time"

	"github.com/anand34577/gorget/client"
	"github.com/anand34577/gorget/client/localapi"
)

// prefFlags registers the settings flags shared by `up` and `set`. Only flags the
// user actually passed are sent, so `gorget up` never resets other settings.
type prefFlags struct {
	fs        *flag.FlagSet
	allowLAN  *bool
	accept    *bool
	useDNS    *bool
	kill      *bool
	advExit   *bool
	pq        *bool
	pmap      *bool
	advRoutes *string
	exitNode  *string
}

func newPrefFlags(fs *flag.FlagSet, withExit bool) *prefFlags {
	p := &prefFlags{fs: fs}
	p.allowLAN = fs.Bool("allow-lan", true, "keep the local network reachable while using an exit node")
	p.accept = fs.Bool("accept-routes", true, "use networks shared by other devices")
	p.useDNS = fs.Bool("use-dns", true, "use Gorget DNS (device names)")
	p.kill = fs.Bool("kill-switch", false, "block traffic outside the tunnel while using an exit node")
	p.pq = fs.Bool("post-quantum", true, "protect connections between Gorget devices against future quantum computers")
	p.pmap = fs.Bool("port-mapping", true, "ask the router to forward our UDP port (UPnP / NAT-PMP / PCP) so direct connections work more often; applies on the next connect")
	p.advExit = fs.Bool("advertise-exit-node", false, "offer this device as an exit node")
	p.advRoutes = fs.String("advertise-routes", "", "share these networks (comma-separated CIDRs; empty to stop)")
	if withExit {
		p.exitNode = fs.String("exit-node", "", "send all internet traffic through this device (name or IP; \"none\" to stop)")
	}
	return p
}

// patch builds the JSON patch for the flags that were set; exit-node names are resolved via st.
func (p *prefFlags) patch(st client.Status) (map[string]any, error) {
	out := map[string]any{}
	var err error
	p.fs.Visit(func(f *flag.Flag) {
		switch f.Name {
		case "allow-lan":
			out["allow_lan"] = *p.allowLAN
		case "accept-routes":
			out["accept_routes"] = *p.accept
		case "use-dns":
			out["use_dns"] = *p.useDNS
		case "kill-switch":
			out["kill_switch"] = *p.kill
		case "port-mapping":
			out["no_port_mapping"] = !*p.pmap
		case "post-quantum":
			out["no_post_quantum"] = !*p.pq
		case "advertise-exit-node":
			out["advertise_exit_node"] = *p.advExit
		case "advertise-routes":
			var routes []string
			for _, r := range strings.Split(*p.advRoutes, ",") {
				if r = strings.TrimSpace(r); r != "" {
					if _, e := netip.ParsePrefix(r); e != nil {
						err = fmt.Errorf("%q is not a network like 192.168.1.0/24", r)
					}
					routes = append(routes, r)
				}
			}
			out["advertise_routes"] = routes
		case "exit-node":
			id, e := resolveExit(st, *p.exitNode)
			if e != nil {
				err = e
			}
			out["exit_node_id"] = id
		}
	})
	return out, err
}

func resolveExit(st client.Status, q string) (string, error) {
	q = strings.TrimSpace(q)
	if q == "" || strings.EqualFold(q, "none") || strings.EqualFold(q, "off") {
		return "", nil
	}
	var hit []client.PeerView
	for _, p := range st.Peers {
		if !p.ExitNode {
			continue
		}
		if strings.EqualFold(p.Name, q) || strings.EqualFold(p.FQDN, q) || p.IPv4 == q || p.IPv6 == q || p.ID == q {
			hit = append(hit, p)
		}
	}
	switch len(hit) {
	case 1:
		return hit[0].ID, nil
	case 0:
		return "", fmt.Errorf("no exit node named %q (see: gorget exit-node)", q)
	}
	return "", fmt.Errorf("%q matches several exit nodes; use the full name or IP", q)
}

// ---------- up / login ----------

func cmdUp(api *localapi.Client, args []string) error {
	fs := flag.NewFlagSet("up", flag.ExitOnError)
	server := fs.String("server", "", "server address, e.g. vpn.example.com")
	key := fs.String("setup-key", "", "register with a setup key instead of the browser (\"-\" reads it from standard input; GORGET_SETUP_KEY also works)")
	pf := newPrefFlags(fs, true)
	_ = fs.Parse(normalizeBoolArgs(args))
	if fs.NArg() > 0 {
		return fmt.Errorf("unexpected argument %q (flags start with -, e.g. -server vpn.example.com)", fs.Arg(0))
	}
	k, err := setupKey(*key)
	if err != nil {
		return err
	}
	st, err := ensureSignedIn(api, *server, k)
	if err != nil {
		return err
	}
	if patch, err := pf.patch(st); err != nil {
		return err
	} else if len(patch) > 0 {
		if _, err := api.SetPrefs(ctx(), patch); err != nil {
			return err
		}
	}
	if err := api.Up(ctx()); err != nil {
		return err
	}
	return waitState(api, 40*time.Second, client.StateRunning, client.StatePendingApproval, client.StateBlocked, client.StateExpired, client.StateDisabled, client.StateNeedsLogin)
}

func cmdLogin(api *localapi.Client, args []string) error {
	fs := flag.NewFlagSet("login", flag.ExitOnError)
	server := fs.String("server", "", "server address")
	key := fs.String("setup-key", "", "setup key (\"-\" reads it from standard input; GORGET_SETUP_KEY also works)")
	_ = fs.Parse(args)
	k, err := setupKey(*key)
	if err != nil {
		return err
	}
	_, err = ensureSignedIn(api, *server, k)
	return err
}

// setupKey returns the setup key from the flag, standard input ("-") or the
// GORGET_SETUP_KEY environment variable. Keys on the command line end up in shell
// history and process lists, so scripts should prefer the other two.
func setupKey(flagVal string) (string, error) {
	switch {
	case flagVal == "-":
		line, err := bufio.NewReader(os.Stdin).ReadString(10)
		if err != nil && line == "" {
			return "", errors.New("no setup key on standard input")
		}
		return strings.TrimSpace(line), nil
	case flagVal != "":
		return strings.TrimSpace(flagVal), nil
	}
	return strings.TrimSpace(os.Getenv("GORGET_SETUP_KEY")), nil
}

// ensureSignedIn points the daemon at the server and completes sign-in when needed.
func ensureSignedIn(api *localapi.Client, server, key string) (client.Status, error) {
	c := ctx()
	st, err := api.Status(c)
	if err != nil {
		return st, err
	}
	if server != "" && server != st.ServerURL {
		if err := api.SetServer(c, server); err != nil {
			return st, err
		}
		if st, err = api.Status(c); err != nil {
			return st, err
		}
	}
	if st.State == client.StateNoServer {
		return st, errors.New("tell me your server first: gorget up -server vpn.example.com")
	}
	if st.State != client.StateNeedsLogin && st.State != client.StateExpired {
		return st, nil
	}
	if key != "" {
		if err := api.LoginSetupKey(c, key); err != nil {
			return st, err
		}
		fmt.Println("Signed in with the setup key.")
		return api.Status(c)
	}
	info, err := api.Login(c)
	if err != nil {
		return st, err
	}
	fmt.Printf("To sign in, open:\n\n  %s\n\n", info.URL)
	if info.Code != "" {
		fmt.Printf("and confirm the code %s.\n", info.Code)
	}
	openBrowser(info.URL)
	fmt.Println("Waiting for you to approve…")
	if err := waitState(api, 10*time.Minute, client.StateStopped, client.StateRunning, client.StateConnecting, client.StatePendingApproval); err != nil {
		return st, err
	}
	return api.Status(c)
}

func openBrowser(u string) {
	var cmd *exec.Cmd
	switch runtime.GOOS {
	case "windows":
		cmd = exec.Command("rundll32", "url.dll,FileProtocolHandler", u)
	case "darwin":
		cmd = exec.Command("open", u)
	default:
		cmd = exec.Command("xdg-open", u)
	}
	_ = cmd.Start() // best effort: the URL is printed anyway
}

// waitState watches status events until one of the states shows up.
func waitState(api *localapi.Client, timeout time.Duration, states ...string) error {
	c, cancel := context.WithTimeout(context.Background(), timeout)
	defer cancel()
	var last client.Status
	err := api.Watch(c, func(st client.Status) {
		last = st
		for _, want := range states {
			if st.State == want {
				cancel()
				return
			}
		}
		// A failed sign-in shows up as an error while still waiting for login.
		if st.State == client.StateNeedsLogin && st.Error != "" && st.LoginURL == "" {
			cancel()
		}
	})
	if err != nil && !errors.Is(err, context.Canceled) && !errors.Is(err, context.DeadlineExceeded) {
		return err
	}
	switch last.State {
	case client.StateRunning:
		if last.Self != nil {
			fmt.Printf("Connected as %s (%s).\n", last.Self.Name, last.Self.IPv4)
		} else {
			fmt.Println("Connected.")
		}
	case client.StatePendingApproval:
		fmt.Println("Signed in. An administrator must approve this device before it can connect.")
	case client.StateBlocked:
		fmt.Println(describe(last))
	case client.StateStopped:
		fmt.Println("Signed in.")
	default:
		if last.Error != "" {
			return errors.New(last.Error)
		}
		if last.State != "" && !contains(states, last.State) {
			return fmt.Errorf("state: %s", last.State)
		}
	}
	return nil
}

func contains(l []string, s string) bool {
	for _, x := range l {
		if x == s {
			return true
		}
	}
	return false
}

// ---------- status ----------

func cmdStatus(api *localapi.Client, args []string) error {
	fs := flag.NewFlagSet("status", flag.ExitOnError)
	asJSON := fs.Bool("json", false, "print JSON")
	watch := fs.Bool("watch", false, "keep printing on every change")
	_ = fs.Parse(args)
	if *watch {
		return api.Watch(context.Background(), func(st client.Status) {
			printStatus(st, *asJSON)
			fmt.Println()
		})
	}
	st, err := api.Status(ctx())
	if err != nil {
		return err
	}
	printStatus(st, *asJSON)
	return nil
}

func printStatus(st client.Status, asJSON bool) {
	if asJSON {
		b, _ := json.MarshalIndent(st, "", "  ")
		fmt.Println(string(b))
		return
	}
	fmt.Println(describe(st))
	if st.Self == nil {
		return
	}
	tw := tabwriter.NewWriter(os.Stdout, 0, 4, 2, ' ', 0)
	fmt.Fprintf(tw, "%s\t%s\t%s\t(this device)\n", st.Self.IPv4, st.Self.Name, st.Self.User)
	for _, p := range st.Peers {
		fmt.Fprintf(tw, "%s\t%s\t%s\t%s\n", p.IPv4, p.Name, p.User, peerLine(p, st.ExitNodeID))
	}
	tw.Flush()
}

func describe(st client.Status) string {
	switch st.State {
	case client.StateNoServer:
		return "Not set up. Run: gorget up -server vpn.example.com"
	case client.StateNeedsLogin:
		s := "Signed out of " + st.ServerURL + ". Run: gorget up"
		if st.Error != "" {
			s += "\n" + st.Error
		}
		return s
	case client.StateStopped:
		return "Disconnected (signed in to " + st.NetworkName + "). Run: gorget up"
	case client.StateConnecting:
		return "Connecting…"
	case client.StateRunning:
		s := "Connected to " + orElse(st.NetworkName, st.ServerURL)
		if st.Notice != "" {
			s += "\n" + st.Notice
		}
		if st.ExitWarning != "" {
			s += "\n! " + st.ExitWarning
		}
		return s
	case client.StatePendingApproval:
		return "Waiting for an administrator to approve this device."
	case client.StateBlocked:
		return "Blocked until this device meets your organisation's security rules.\n" + st.Error
	case client.StateExpired, client.StateDisabled:
		return orElse(st.Error, st.State)
	}
	return st.State
}

func peerLine(p client.PeerView, exitID string) string {
	var parts []string
	switch {
	case !p.Online:
		parts = append(parts, "offline")
	case p.Direct:
		parts = append(parts, fmt.Sprintf("direct %dms", p.LatencyMs))
	case p.RxBytes > 0 || p.TxBytes > 0 || p.LastHandshake > 0:
		parts = append(parts, "relay")
	default:
		parts = append(parts, "idle")
	}
	if p.PostQuantum {
		parts = append(parts, "post-quantum")
	}
	if p.ID == exitID {
		parts = append(parts, "exit node (in use)")
	} else if p.ExitNode {
		parts = append(parts, "offers exit node")
	}
	if len(p.Routes) > 0 {
		parts = append(parts, "routes "+strings.Join(p.Routes, ","))
	}
	if p.RxBytes+p.TxBytes > 0 {
		parts = append(parts, fmt.Sprintf("rx %s tx %s", bytesStr(p.RxBytes), bytesStr(p.TxBytes)))
	}
	return strings.Join(parts, "; ")
}

func bytesStr(n uint64) string {
	const unit = 1024
	if n < unit {
		return fmt.Sprintf("%dB", n)
	}
	v, suf := float64(n), "KMGT"
	i := -1
	for v >= unit && i < len(suf)-1 {
		v /= unit
		i++
	}
	return fmt.Sprintf("%.1f%ciB", v, suf[i])
}

func orElse(a, b string) string {
	if a != "" {
		return a
	}
	return b
}

// ---------- exit-node, set, netcheck, operator ----------

func cmdExitNode(api *localapi.Client, args []string) error {
	st, err := api.Status(ctx())
	if err != nil {
		return err
	}
	if len(args) == 0 {
		tw := tabwriter.NewWriter(os.Stdout, 0, 4, 2, ' ', 0)
		n := 0
		for _, p := range st.Peers {
			if !p.ExitNode {
				continue
			}
			n++
			use := ""
			if p.ID == st.ExitNodeID {
				use = "in use"
			}
			state := "online"
			if !p.Online {
				state = "offline"
			}
			fmt.Fprintf(tw, "%s\t%s\t%s\t%s\n", p.IPv4, p.Name, state, use)
		}
		tw.Flush()
		if n == 0 {
			fmt.Println("No device offers to be an exit node.")
		}
		return nil
	}
	var id string
	switch args[0] {
	case "none", "off":
	case "set", "use":
		if len(args) < 2 {
			return errors.New("which device? gorget exit-node set NAME")
		}
		if id, err = resolveExit(st, args[1]); err != nil {
			return err
		}
	default:
		if id, err = resolveExit(st, args[0]); err != nil {
			return err
		}
	}
	if _, err := api.SetPrefs(ctx(), map[string]any{"exit_node_id": id}); err != nil {
		return err
	}
	if id == "" {
		fmt.Println("Exit node off.")
	} else {
		fmt.Println("Sending internet traffic through", args[len(args)-1])
	}
	return nil
}

func cmdSet(api *localapi.Client, args []string) error {
	fs := flag.NewFlagSet("set", flag.ExitOnError)
	pf := newPrefFlags(fs, true)
	_ = fs.Parse(normalizeBoolArgs(args))
	st, err := api.Status(ctx())
	if err != nil {
		return err
	}
	patch, err := pf.patch(st)
	if err != nil {
		return err
	}
	if len(patch) == 0 {
		return errors.New("nothing to change; see: gorget help")
	}
	_, err = api.SetPrefs(ctx(), patch)
	return err
}

// normalizeBoolArgs lets "-allow-lan false" work as well as "-allow-lan=false".
func normalizeBoolArgs(args []string) []string {
	var out []string
	for i := 0; i < len(args); i++ {
		a := args[i]
		if i+1 < len(args) && !strings.Contains(a, "=") && (args[i+1] == "true" || args[i+1] == "false") && strings.HasPrefix(a, "-") {
			out = append(out, a+"="+args[i+1])
			i++
			continue
		}
		out = append(out, a)
	}
	return out
}

func cmdNetcheck(api *localapi.Client) error {
	st, err := api.Status(ctx())
	if err != nil {
		return err
	}
	if st.State != client.StateRunning {
		return errors.New("not connected; run: gorget up")
	}
	fmt.Println("Server:        ", st.ServerURL)
	fmt.Println("Relay:         ", yesNo(st.RelayUp, "connected "+st.RelayURL+" ("+orElse(st.RelayTransport, "?")+")", "not connected"))
	if len(st.Endpoints) == 0 {
		fmt.Println("Public address: none discovered (STUN blocked? direct connections need UDP)")
	} else {
		fmt.Println("Addresses:     ", strings.Join(st.Endpoints, ", "))
	}
	direct, relayed, off := 0, 0, 0
	for _, p := range st.Peers {
		switch {
		case !p.Online:
			off++
		case p.Direct:
			direct++
		default:
			relayed++
		}
	}
	fmt.Printf("Devices:        %d direct, %d relayed or idle, %d offline\n", direct, relayed, off)
	switch {
	case !st.RelayUp:
		fmt.Println("\nThe relay isn't connected: devices that can't reach each other directly will fail. Check that TCP 443 to the server is open.")
	case relayed > 0 && direct == 0 && len(st.Endpoints) == 0:
		fmt.Println("\nNo direct paths and no discovered address: this network probably blocks UDP. Traffic works through the relay but is slower.")
	}
	return nil
}

func yesNo(b bool, y, n string) string {
	if b {
		return y
	}
	return n
}

func cmdOperator(api *localapi.Client, args []string) error {
	if len(args) == 0 {
		w, err := api.WhoAmI(ctx())
		if err != nil {
			return err
		}
		fmt.Printf("operator: %s\nyou are: %s (admin: %v, can control: %v)\n", orElse(w.Operator, "(none)"), orElse(w.User, "unknown"), w.Admin, w.CanControl)
		return nil
	}
	dir := localapi.DefaultDataDir()
	switch args[0] {
	case "set":
		if len(args) < 2 {
			return errors.New("which user? gorget operator set USER")
		}
		if err := localapi.SetOperator(dir, args[1]); err != nil {
			return fmt.Errorf("%w (run as root/Administrator)", err)
		}
		fmt.Println("operator set to", args[1])
	case "clear":
		if err := localapi.SetOperator(dir, ""); err != nil {
			return fmt.Errorf("%w (run as root/Administrator)", err)
		}
		fmt.Println("operator cleared")
	default:
		return errors.New("usage: gorget operator [set USER | clear]")
	}
	return nil
}
