package main

import (
	"bufio"
	"context"
	"errors"
	"flag"
	"fmt"
	"net"
	"net/http"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"time"

	"gopkg.in/yaml.v3"

	"github.com/anand34577/gorget/internal/config"
)

// ---------- init: guided first configuration ----------

// cmdInit asks a few questions, writes a small configuration file (everything not
// asked keeps its default) and checks the machine before anything is installed.
func cmdInit(args []string) error {
	fs := flag.NewFlagSet("init", flag.ExitOnError)
	path := fs.String("config", defaultInitPath(), "where to write the configuration file")
	force := fs.Bool("force", false, "overwrite an existing configuration file")
	// Flags that answer the questions, for scripts and for people who want no prompts.
	yesAll := fs.Bool("yes", false, "don't ask: use the flags below and the defaults (needs -domain)")
	domainF := fs.String("domain", "", "domain name of this server, e.g. vpn.example.com")
	tlsF := fs.String("tls", "", "HTTPS: acme (Let's Encrypt), proxy (behind a reverse proxy) or private (own CA)")
	emailF := fs.String("email", "", "email for certificate expiry notices (acme only)")
	dbF := fs.String("database-url", "", "PostgreSQL URL (default: SQLite)")
	gatewayF := fs.String("gateway", "", "yes or no: let standard WireGuard apps connect (default yes; no on Windows)")
	dataF := fs.String("data-dir", "", "where to keep data (database, keys, backups)")
	_ = fs.Parse(args)

	if _, err := os.Stat(*path); err == nil && !*force {
		return fmt.Errorf("%s already exists. Check it with: gorget-server check -config %s (or rerun init with -force to start over)", *path, *path)
	}
	in := bufio.NewReader(os.Stdin)
	scripted := *yesAll || *domainF != ""
	if !scripted {
		fmt.Println("Gorget server setup. Press Enter to accept the suggestion in [brackets].")
		fmt.Println()
	}
	// answer returns the flag value when given, the default when running without prompts,
	// and otherwise asks.
	answer := func(flagVal, prompt, def string) string {
		switch {
		case flagVal != "":
			return flagVal
		case scripted:
			return def
		}
		return ask(in, prompt, def)
	}

	// 1. Address.
	var host string
	for {
		host = answer(*domainF, "1/5  Domain name of this server (people and apps use it), e.g. vpn.example.com", "")
		host = strings.TrimSuffix(strings.TrimPrefix(strings.TrimPrefix(strings.TrimSpace(host), "https://"), "http://"), "/")
		msg := ""
		switch {
		case host == "":
			msg = "A domain (or for a private test, this machine's IP address) is required."
		case strings.ContainsAny(host, " /"):
			msg = "Enter only the name, like vpn.example.com."
		}
		if msg == "" {
			break
		}
		if scripted {
			return errors.New("-domain: " + msg)
		}
		fmt.Println("     " + msg)
	}
	isIP := net.ParseIP(strings.Trim(host, "[]")) != nil

	// 2. HTTPS.
	if !scripted {
		fmt.Println()
		fmt.Println("2/5  How should HTTPS work?")
		fmt.Println("     1) Gorget gets a free certificate from Let's Encrypt (recommended; ports 80 and 443 must reach this machine)")
		fmt.Println("     2) A reverse proxy (Caddy, nginx, Traefik) in front handles HTTPS; Gorget listens on 127.0.0.1:8080")
		fmt.Println("     3) Private or test setup: Gorget makes its own certificate authority (browsers warn until you trust it)")
		if isIP {
			fmt.Println("     (Let's Encrypt needs a domain name, so 1 is not available for an IP address.)")
		}
	}
	def := "1"
	if isIP {
		def = "3"
	}
	mode := ""
	for mode == "" {
		choice := answer(*tlsF, "     Choice", def)
		switch strings.ToLower(choice) {
		case "1", "acme", "letsencrypt":
			if isIP {
				if scripted {
					return errors.New("-tls acme needs a domain name; Let's Encrypt can't issue certificates for IP addresses (use -tls private)")
				}
				fmt.Println("     Let's Encrypt can't issue certificates for IP addresses; choose 2 or 3.")
				continue
			}
			mode = config.TLSModeACME
		case "2", "proxy", "off":
			mode = config.TLSModeOff
		case "3", "private", "internal-ca":
			mode = config.TLSModeInternalCA
		default:
			if scripted {
				return fmt.Errorf("-tls %q: use acme, proxy or private", choice)
			}
			fmt.Println("     Type 1, 2 or 3.")
		}
	}
	email := ""
	if mode == config.TLSModeACME {
		email = answer(*emailF, "     Email for certificate expiry notices (optional)", "")
	}

	// 3. Database.
	if !scripted {
		fmt.Println()
		fmt.Println("3/5  Database: SQLite needs no setup and suits most networks. Use PostgreSQL for")
		fmt.Println("     very large networks or to run several servers together.")
	}
	dsn := ""
	for {
		dsn = answer(*dbF, "     PostgreSQL URL (leave empty for SQLite)", "")
		if dsn == "" || strings.HasPrefix(dsn, "postgres://") || strings.HasPrefix(dsn, "postgresql://") {
			break
		}
		if scripted {
			return errors.New("-database-url should start with postgres://user:password@host/dbname")
		}
		fmt.Println("     It should start with postgres://user:password@host/dbname")
	}

	// 4. Gateway.
	if !scripted {
		fmt.Println()
	}
	gwDefault := "y"
	if runtime.GOOS == "windows" {
		gwDefault = "n"
		if !scripted {
			fmt.Println("4/5  The gateway for standard WireGuard apps needs Linux or macOS; it stays off on Windows.")
		}
	}
	gateway := false
	if runtime.GOOS != "windows" {
		gateway = yes(answer(*gatewayF, "4/5  Let standard WireGuard apps connect too (opens UDP 51820)? y/n", gwDefault))
	}

	// 5. Data directory.
	if !scripted {
		fmt.Println()
	}
	dataDir := answer(*dataF, "5/5  Where to keep data (database, keys, backups)", config.Default().DataDir)

	// Build the file: only what differs from the defaults, so upgrades bring new defaults.
	scheme := "https"
	doc := map[string]any{
		"public_url": scheme + "://" + host,
		"data_dir":   dataDir,
		"tls":        map[string]any{"mode": mode},
	}
	if email != "" {
		doc["tls"].(map[string]any)["email"] = email
	}
	if mode == config.TLSModeOff {
		doc["http"] = map[string]any{"listen": "127.0.0.1:8080", "redirect_listen": "", "http3": false, "trusted_proxies": []string{"127.0.0.1/32", "::1/128"}}
	}
	if dsn != "" {
		doc["database"] = map[string]any{"driver": "postgres", "dsn": dsn}
	}
	doc["gateway"] = map[string]any{"enabled": gateway}

	b, err := yaml.Marshal(doc)
	if err != nil {
		return err
	}
	header := "# Gorget server configuration, written by `gorget-server init`.\n" +
		"# Every other option keeps its default; see `gorget-server config-example` for all of them.\n"
	if err := os.MkdirAll(filepath.Dir(*path), 0o755); err != nil {
		return fmt.Errorf("create %s: %w (run as root/Administrator, or pass -config with a path you can write)", filepath.Dir(*path), err)
	}
	// 0600: the file may hold a database password.
	if err := os.WriteFile(*path, append([]byte(header), b...), 0o600); err != nil {
		return fmt.Errorf("write %s: %w (run as root/Administrator, or pass -config with a path you can write)", *path, err)
	}
	fmt.Printf("\nSaved %s\n\nChecking this machine…\n\n", *path)

	cfg, err := config.Load(*path)
	if err == nil {
		err = cfg.Finalize()
	}
	if err != nil {
		return fmt.Errorf("the new configuration is invalid: %w", err)
	}
	ok := runChecks(cfg)
	fmt.Println()
	if !ok {
		fmt.Println("Fix the items marked ✗, then run: gorget-server check -config " + *path)
	}
	fmt.Println("Next steps:")
	fmt.Printf("  1. Install and start the service:  %sgorget-server install -config %s\n", sudo(), *path)
	fmt.Println("     It prints your setup link when it is up; open it and create the owner account.")
	fmt.Printf("     (Lost the link? Run: %sgorget-server setup-link)\n", sudo())
	if mode == config.TLSModeOff {
		fmt.Println("  Point your reverse proxy at http://127.0.0.1:8080 (examples in deploy/proxy: Caddyfile, nginx.conf).")
	}
	return nil
}

func defaultInitPath() string {
	if p := defaultConfigPath(); p != "" {
		return p
	}
	if runtime.GOOS == "windows" {
		return filepath.Join(os.Getenv("ProgramData"), "Gorget", "config.yaml")
	}
	return "/etc/gorget/config.yaml"
}

func ask(in *bufio.Reader, prompt, def string) string {
	if def != "" {
		fmt.Printf("%s [%s]: ", prompt, def)
	} else {
		fmt.Printf("%s: ", prompt)
	}
	line, err := in.ReadString('\n')
	line = strings.TrimSpace(line)
	if err != nil && line == "" {
		// End of input (piped or closed): take the default rather than looping forever.
		fmt.Println()
		return def
	}
	if line == "" {
		return def
	}
	return line
}

func yes(s string) bool {
	s = strings.ToLower(strings.TrimSpace(s))
	return s == "y" || s == "yes"
}

func sudo() string {
	if runtime.GOOS == "windows" {
		return "" // run from an Administrator terminal
	}
	return "sudo "
}

func logHint() string {
	switch runtime.GOOS {
	case "linux":
		return "journalctl -u gorget-server"
	case "darwin":
		return "/usr/local/var/log/gorget-server.err.log"
	case "windows":
		return "Event Viewer > Windows Logs > Application, or " + filepath.Join(os.Getenv("ProgramData"), "Gorget", "setup-token")
	}
	return "the service log"
}

// ---------- check: is this machine ready? ----------

func cmdCheck(args []string) error {
	fs := flag.NewFlagSet("check", flag.ExitOnError)
	var cf commonFlags
	cf.register(fs)
	_ = fs.Parse(args)
	cfg, err := cf.load()
	if err != nil {
		fmt.Println("✗ configuration:", err)
		if cf.configPath == "" {
			fmt.Println("  No configuration file found. Create one with: " + sudo() + "gorget-server init")
		}
		return errors.New("fix the configuration first")
	}
	fmt.Println("✓ configuration is valid")
	if !runChecks(cfg) {
		return errors.New("some checks failed (marked ✗ above)")
	}
	fmt.Println("\nEverything needed looks fine.")
	return nil
}

type checker struct{ failed bool }

func (c *checker) ok(msg string)   { fmt.Println("✓", msg) }
func (c *checker) warn(msg string) { fmt.Println("!", msg) }
func (c *checker) fail(msg string) { fmt.Println("✗", msg); c.failed = true }

// runChecks reports problems that would stop the server or clients from working.
func runChecks(cfg config.Config) bool {
	c := &checker{}
	running := serverAnswers(cfg)
	if running {
		c.ok("a Gorget server is already running here (ports in use by it are expected)")
	}

	// Data directory.
	if err := os.MkdirAll(cfg.DataDir, 0o700); err != nil {
		c.fail(fmt.Sprintf("data directory %s can't be created: %v (run as root/Administrator)", cfg.DataDir, err))
	} else if f, err := os.CreateTemp(cfg.DataDir, ".write-test-*"); err != nil {
		c.fail(fmt.Sprintf("data directory %s isn't writable: %v", cfg.DataDir, err))
	} else {
		f.Close()
		os.Remove(f.Name())
		c.ok("data directory " + cfg.DataDir + " is writable")
	}

	// DNS for the public name.
	host := cfg.PublicHost()
	if ip := net.ParseIP(host); ip == nil && host != "" {
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		addrs, err := net.DefaultResolver.LookupHost(ctx, host)
		cancel()
		switch {
		case err != nil:
			c.fail(fmt.Sprintf("%s doesn't resolve (%v). Create a DNS A/AAAA record pointing at this server's public address", host, err))
		case anyLocal(addrs):
			c.ok(fmt.Sprintf("%s points at this machine (%s)", host, strings.Join(addrs, ", ")))
		default:
			c.warn(fmt.Sprintf("%s points at %s, which isn't an address of this machine. That's fine behind a router or cloud NAT if ports are forwarded; otherwise fix the DNS record", host, strings.Join(addrs, ", ")))
		}
	}

	// Ports.
	if !running {
		type port struct{ network, addr, what string }
		ports := []port{{"tcp", cfg.HTTP.Listen, "web console and apps (HTTPS)"}}
		if cfg.HTTP.RedirectListen != "" {
			ports = append(ports, port{"tcp", cfg.HTTP.RedirectListen, "HTTP redirects and Let's Encrypt checks"})
		}
		if cfg.STUN.Enabled {
			ports = append(ports, port{"udp", cfg.STUN.Listen, "STUN (direct connections)"})
		}
		if cfg.Relay.Enabled && cfg.Relay.UDPListen != "" {
			ports = append(ports, port{"udp", cfg.Relay.UDPListen, "UDP relay"})
		}
		if cfg.Gateway.Enabled && runtime.GOOS != "windows" {
			ports = append(ports, port{"udp", fmt.Sprintf(":%d", cfg.Gateway.ListenPort), "WireGuard gateway"})
		}
		for _, p := range ports {
			if err := canListen(p.network, p.addr); err != nil {
				hint := "another program uses it; stop that program or change the port in the configuration"
				if errors.Is(err, os.ErrPermission) || strings.Contains(err.Error(), "permission") || strings.Contains(err.Error(), "access") {
					hint = "ports below 1024 need root/Administrator (the installed service has it)"
				}
				c.fail(fmt.Sprintf("%s %s (%s): %v. %s", strings.ToUpper(p.network), p.addr, p.what, err, hint))
			} else {
				c.ok(fmt.Sprintf("%s %s is free (%s)", strings.ToUpper(p.network), p.addr, p.what))
			}
		}
	}

	// TLS specifics.
	switch cfg.TLS.Mode {
	case config.TLSModeACME:
		if cfg.HTTP.RedirectListen == "" && !strings.HasSuffix(cfg.HTTP.Listen, ":443") {
			c.warn("Let's Encrypt validates over port 80 or 443; with neither, certificates can't be issued. Use acme-dns, custom or off")
		} else {
			c.ok("Let's Encrypt: make sure TCP 80 and 443 reach this machine from the internet (firewall, cloud security group, router)")
		}
	case config.TLSModeOff:
		c.ok("TLS is off: a reverse proxy must terminate HTTPS and forward to " + cfg.HTTP.Listen)
		if len(cfg.HTTP.TrustedProxies) == 0 {
			c.warn("http.trusted_proxies is empty, so client addresses in logs and rate limits will be the proxy's")
		}
	case config.TLSModeInternalCA:
		c.warn("internal CA: browsers and apps warn until the CA certificate (in the data directory) is trusted on each device")
	}

	// Gateway prerequisites.
	if cfg.Gateway.Enabled {
		switch runtime.GOOS {
		case "linux":
			if _, err := exec.LookPath("nft"); err != nil {
				c.fail("the WireGuard gateway needs nftables: install the nftables package (or set gateway.enabled: false)")
			} else {
				c.ok("nftables found for the WireGuard gateway")
			}
			if b, err := os.ReadFile("/proc/sys/net/ipv4/ip_forward"); err == nil && strings.TrimSpace(string(b)) != "1" {
				c.ok("IP forwarding is off now; the server turns it on when the gateway starts")
			}
		case "windows":
			c.warn("the WireGuard gateway isn't available on Windows; standard WireGuard apps can't connect (Gorget apps can)")
		}
	}
	if runtime.GOOS != "windows" && os.Geteuid() != 0 && !running {
		c.warn("not running as root: the service needs root (or CAP_NET_ADMIN + CAP_NET_BIND_SERVICE) for ports 80/443 and the gateway")
	}
	return !c.failed
}

func anyLocal(addrs []string) bool {
	ifAddrs, err := net.InterfaceAddrs()
	if err != nil {
		return false
	}
	local := map[string]bool{}
	for _, a := range ifAddrs {
		if ipn, ok := a.(*net.IPNet); ok {
			local[ipn.IP.String()] = true
		}
	}
	for _, a := range addrs {
		if local[a] {
			return true
		}
	}
	return false
}

func canListen(network, addr string) error {
	switch network {
	case "tcp":
		ln, err := net.Listen("tcp", addr)
		if err != nil {
			return err
		}
		return ln.Close()
	default:
		pc, err := net.ListenPacket("udp", addr)
		if err != nil {
			return err
		}
		return pc.Close()
	}
}

// serverAnswers reports whether a Gorget server already answers on this machine.
func serverAnswers(cfg config.Config) bool {
	_, port, err := net.SplitHostPort(cfg.HTTP.Listen)
	if err != nil {
		return false
	}
	scheme := "https"
	if cfg.TLS.Mode == config.TLSModeOff {
		scheme = "http"
	}
	u := url.URL{Scheme: scheme, Host: net.JoinHostPort("127.0.0.1", port), Path: "/healthz"}
	hc := &http.Client{Timeout: 2 * time.Second, Transport: insecureLocalTransport()}
	resp, err := hc.Get(u.String())
	if err != nil {
		return false
	}
	resp.Body.Close()
	return resp.StatusCode == http.StatusOK
}

// ---------- setup link ----------

// setupLink returns the first-run link for this server, or "" once setup is finished.
func setupLink(cfg config.Config) string {
	b, err := os.ReadFile(filepath.Join(cfg.DataDir, "setup-token"))
	if err != nil || strings.TrimSpace(string(b)) == "" {
		return ""
	}
	return strings.TrimSuffix(cfg.PublicURL, "/") + "/setup#token=" + strings.TrimSpace(string(b))
}

// waitSetupLink waits for a freshly started service to write its setup token and prints the link.
func waitSetupLink(cfg config.Config) {
	deadline := time.Now().Add(30 * time.Second)
	for time.Now().Before(deadline) {
		if link := setupLink(cfg); link != "" {
			fmt.Println()
			fmt.Println("Gorget is running. Finish setting it up in your browser:")
			fmt.Println()
			fmt.Println("  " + link)
			fmt.Println()
			fmt.Println("Create the owner account there. The link works until setup is done.")
			if cfg.TLS.Mode == config.TLSModeACME {
				fmt.Println("The first visit can take a few seconds while the HTTPS certificate is issued.")
			}
			return
		}
		time.Sleep(500 * time.Millisecond)
	}
	fmt.Println("The service is starting but hasn't written its setup link yet. Check the log: " + logHint())
	fmt.Println("Then run: gorget-server setup-link")
}

// cmdSetupLink prints the setup link again (it is also in the service log).
func cmdSetupLink(args []string) error {
	fs := flag.NewFlagSet("setup-link", flag.ExitOnError)
	var cf commonFlags
	cf.register(fs)
	_ = fs.Parse(args)
	cfg, err := cf.load()
	if err != nil {
		return err
	}
	if link := setupLink(cfg); link != "" {
		fmt.Println(link)
		return nil
	}
	fmt.Println("No setup is pending: the owner account already exists. Sign in at " + cfg.PublicURL)
	fmt.Println("Locked out? Run: gorget-server reset-password -email you@example.com")
	return nil
}
