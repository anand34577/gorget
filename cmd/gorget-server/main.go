// Command gorget-server runs the Gorget control plane, relay, STUN server,
// WireGuard gateway and web console.
package main

import (
	"context"
	"crypto/tls"
	"encoding/base64"
	"errors"
	"flag"
	"fmt"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"path/filepath"
	"runtime"
	"strings"
	"syscall"
	"time"

	"github.com/kardianos/service"

	"github.com/anand34577/gorget/internal/app"
	"github.com/anand34577/gorget/internal/backup"
	"github.com/anand34577/gorget/internal/config"
	"github.com/anand34577/gorget/internal/core"
	"github.com/anand34577/gorget/internal/secrets"
	"github.com/anand34577/gorget/internal/store"
)

const usage = `gorget-server — self-hosted WireGuard mesh VPN server

Usage:
  gorget-server [command] [flags]

First time? Run:  gorget-server init     (asks a few questions, writes the config)
           then:  gorget-server install  (installs, starts, and prints your setup link)
No questions:     gorget-server init -yes -domain vpn.example.com

Commands:
  init              Guided setup: write a configuration file and check this machine
  check             Check the configuration, DNS, ports and prerequisites
  serve             Run the server (default)
  install           Install as a system service (systemd / Windows service / launchd)
  uninstall         Remove the system service
  start | stop | restart   Control the installed service
  setup-link        Print the first-run setup link again
  backup            Write an encrypted backup:     backup -out file.gbk
  restore           Restore into an empty database: restore -in file.gbk
  migrate-db        Copy all data to another database: migrate-db -to postgres://...
  reset-password    Set a temporary password:       reset-password -email you@example.com
  config-example    Print an annotated configuration file
  gen-master-key    Print a new random master key
  healthcheck       Exit 0 if the local server answers /healthz (for Docker)
  version           Print the version

Common flags:
  -config path      Configuration file (default: $GORGET_CONFIG, /etc/gorget/config.yaml or
                    %ProgramData%\Gorget\config.yaml when present)
  -public-url URL   Public URL (e.g. https://vpn.example.com)
  -data-dir dir     Data directory
`

func main() {
	args := os.Args[1:]
	cmd := "serve"
	if len(args) > 0 && !strings.HasPrefix(args[0], "-") {
		cmd, args = args[0], args[1:]
	}
	var err error
	switch cmd {
	case "serve", "run":
		err = cmdServe(args)
	case "init", "setup":
		err = cmdInit(args)
	case "setup-link":
		err = cmdSetupLink(args)
	case "check", "doctor":
		err = cmdCheck(args)
	case "install", "uninstall", "start", "stop", "restart":
		err = cmdService(cmd, args)
	case "backup":
		err = cmdBackup(args)
	case "restore":
		err = cmdRestore(args)
	case "migrate-db":
		err = cmdMigrate(args)
	case "reset-password":
		err = cmdResetPassword(args)
	case "config-example":
		fmt.Print(config.Example())
	case "gen-master-key":
		fmt.Println(base64.StdEncoding.EncodeToString(secrets.RandomBytes(32)))
	case "healthcheck":
		err = cmdHealthcheck(args)
	case "version", "-v", "--version":
		fmt.Printf("gorget-server %s (%s/%s, %s)\n", core.Version, runtime.GOOS, runtime.GOARCH, runtime.Version())
	case "help", "-h", "--help":
		fmt.Print(usage)
	default:
		fmt.Fprintf(os.Stderr, "unknown command %q\n\n%s", cmd, usage)
		os.Exit(2)
	}
	if err != nil {
		fmt.Fprintln(os.Stderr, "error:", err)
		os.Exit(1)
	}
}

type commonFlags struct {
	configPath string
	publicURL  string
	dataDir    string
	listen     string
	tlsMode    string
	dbDriver   string
	dbDSN      string
}

func (f *commonFlags) register(fs *flag.FlagSet) {
	fs.StringVar(&f.configPath, "config", defaultConfigPath(), "configuration file")
	fs.StringVar(&f.publicURL, "public-url", "", "public URL, e.g. https://vpn.example.com")
	fs.StringVar(&f.dataDir, "data-dir", "", "data directory")
	fs.StringVar(&f.listen, "listen", "", "HTTPS listen address (default :443)")
	fs.StringVar(&f.tlsMode, "tls-mode", "", "acme | acme-dns | custom | internal-ca | off")
	fs.StringVar(&f.dbDriver, "db", "", "database driver: sqlite | postgres")
	fs.StringVar(&f.dbDSN, "dsn", "", "database DSN / file")
}

func defaultConfigPath() string {
	if v := os.Getenv("GORGET_CONFIG"); v != "" {
		return v
	}
	candidates := []string{"/etc/gorget/config.yaml"}
	if runtime.GOOS == "windows" {
		candidates = []string{filepath.Join(os.Getenv("ProgramData"), "Gorget", "config.yaml")}
	}
	for _, c := range candidates {
		if _, err := os.Stat(c); err == nil {
			return c
		}
	}
	return ""
}

func (f *commonFlags) load() (config.Config, error) {
	cfg, err := config.Load(f.configPath)
	if err != nil {
		return cfg, err
	}
	if f.publicURL != "" {
		cfg.PublicURL = f.publicURL
	}
	if f.dataDir != "" {
		cfg.DataDir = f.dataDir
		// Re-derive defaults that depend on the data dir.
		if cfg.Database.Driver == "sqlite" && f.dbDSN == "" {
			cfg.Database.DSN = ""
		}
	}
	if f.listen != "" {
		cfg.HTTP.Listen = f.listen
	}
	if f.tlsMode != "" {
		cfg.TLS.Mode = f.tlsMode
	}
	if f.dbDriver != "" {
		cfg.Database.Driver = f.dbDriver
	}
	if f.dbDSN != "" {
		cfg.Database.DSN = f.dbDSN
	}
	return cfg, cfg.Finalize()
}

func newLogger(cfg config.Config) *slog.Logger {
	var lvl slog.Level
	_ = lvl.UnmarshalText([]byte(cfg.LogLevel))
	opts := &slog.HandlerOptions{Level: lvl}
	if cfg.LogFormat == "json" {
		return slog.New(slog.NewJSONHandler(os.Stderr, opts))
	}
	return slog.New(slog.NewTextHandler(os.Stderr, opts))
}

// ---------- serve & service ----------

type program struct {
	cfg    config.Config
	log    *slog.Logger
	cancel context.CancelFunc
	done   chan error
}

func (p *program) Start(s service.Service) error {
	ctx, cancel := context.WithCancel(context.Background())
	p.cancel, p.done = cancel, make(chan error, 1)
	go func() {
		err := app.Run(ctx, p.cfg, p.log)
		p.done <- err
		if err != nil && !service.Interactive() {
			p.log.Error("server exited", "err", err)
			os.Exit(1)
		}
	}()
	return nil
}

func (p *program) Stop(s service.Service) error {
	p.cancel()
	select {
	case <-p.done:
	case <-time.After(20 * time.Second):
	}
	return nil
}

func serviceConfig(args []string) *service.Config {
	return &service.Config{
		Name:        "gorget-server",
		DisplayName: "Gorget VPN Server",
		Description: "Gorget self-hosted WireGuard mesh VPN control plane, relay and gateway.",
		Arguments:   append([]string{"serve"}, args...),
		Option: service.KeyValue{
			"Restart":                "on-failure",
			"LimitNOFILE":            65536,
			"SystemdScript":          systemdUnit,
			"OnFailure":              "restart",
			"OnFailureDelayDuration": "5s",
			"DelayedAutoStart":       false,
		},
	}
}

func cmdServe(args []string) error {
	fs := flag.NewFlagSet("serve", flag.ExitOnError)
	var cf commonFlags
	cf.register(fs)
	_ = fs.Parse(args)
	cfg, err := cf.load()
	if err != nil {
		return err
	}
	log := newLogger(cfg)
	if service.Interactive() {
		ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
		defer stop()
		return app.Run(ctx, cfg, log)
	}
	prg := &program{cfg: cfg, log: log}
	s, err := service.New(prg, serviceConfig(args))
	if err != nil {
		return err
	}
	return s.Run()
}

func cmdService(action string, args []string) error {
	var cfg config.Config
	if action == "install" {
		fs := flag.NewFlagSet("install", flag.ExitOnError)
		var cf commonFlags
		cf.register(fs)
		_ = fs.Parse(args)
		var err error
		if cfg, err = cf.load(); err != nil {
			return fmt.Errorf("configuration is invalid, fix it before installing: %w", err)
		}
	}
	s, err := service.New(&program{}, serviceConfig(args))
	if err != nil {
		return err
	}
	if err := service.Control(s, action); err != nil {
		if action == "install" && strings.Contains(strings.ToLower(err.Error()), "already exists") {
			return errors.New("the service is already installed; use: gorget-server restart (or uninstall first to change its flags)")
		}
		if strings.Contains(strings.ToLower(err.Error()), "access") || strings.Contains(strings.ToLower(err.Error()), "permission") {
			return fmt.Errorf("%w (run as root/Administrator)", err)
		}
		return err
	}
	fmt.Printf("service %s: ok\n", action)
	if action == "install" {
		if err := service.Control(s, "start"); err != nil {
			fmt.Fprintln(os.Stderr, "installed, but it didn't start:", err)
			fmt.Fprintln(os.Stderr, "check the log: "+logHint())
			return nil
		}
		waitSetupLink(cfg)
	}
	return nil
}

// ---------- maintenance commands ----------

func cmdBackup(args []string) error {
	fs := flag.NewFlagSet("backup", flag.ExitOnError)
	var cf commonFlags
	cf.register(fs)
	out := fs.String("out", "", "output file (default <data_dir>/backups/gorget-<time>.gbk)")
	_ = fs.Parse(args)
	cfg, err := cf.load()
	if err != nil {
		return err
	}
	ctx := context.Background()
	st, box, err := app.OpenStore(ctx, cfg)
	if err != nil {
		return err
	}
	defer st.Close()
	d, err := backup.Export(ctx, st)
	if err != nil {
		return err
	}
	path := *out
	if path == "" {
		path = filepath.Join(cfg.Backup.Dir, "gorget-"+time.Now().UTC().Format("20060102-150405")+".gbk")
	}
	if err := backup.Write(d, box, path); err != nil {
		return err
	}
	fmt.Println("backup written:", path)
	fmt.Println("keep your master key safe — it is required to restore:", cfg.Security.MasterKeyFile)
	return nil
}

func cmdRestore(args []string) error {
	fs := flag.NewFlagSet("restore", flag.ExitOnError)
	var cf commonFlags
	cf.register(fs)
	in := fs.String("in", "", "backup file")
	_ = fs.Parse(args)
	if *in == "" {
		return errors.New("-in is required")
	}
	cfg, err := cf.load()
	if err != nil {
		return err
	}
	ctx := context.Background()
	st, box, err := app.OpenStore(ctx, cfg)
	if err != nil {
		return err
	}
	defer st.Close()
	d, err := backup.Read(*in, box)
	if err != nil {
		return err
	}
	if err := backup.Import(ctx, st, d); err != nil {
		return err
	}
	fmt.Println("restore complete; start the server normally")
	return nil
}

func cmdMigrate(args []string) error {
	fs := flag.NewFlagSet("migrate-db", flag.ExitOnError)
	var cf commonFlags
	cf.register(fs)
	to := fs.String("to", "", "target PostgreSQL URL (postgres://user:pass@host/db)")
	toDriver := fs.String("to-driver", "postgres", "target driver")
	_ = fs.Parse(args)
	if *to == "" {
		return errors.New("-to is required")
	}
	cfg, err := cf.load()
	if err != nil {
		return err
	}
	ctx := context.Background()
	src, _, err := app.OpenStore(ctx, cfg)
	if err != nil {
		return err
	}
	defer src.Close()
	dst, err := store.Open(ctx, *toDriver, *to, 5)
	if err != nil {
		return fmt.Errorf("open target: %w", err)
	}
	defer dst.Close()
	d, err := backup.Export(ctx, src)
	if err != nil {
		return err
	}
	if err := backup.Import(ctx, dst, d); err != nil {
		return err
	}
	fmt.Printf("migrated %d tables to %s. Update your configuration: database.driver=%s, database.dsn=<target>\n", len(store.Tables), *toDriver, *toDriver)
	return nil
}

func cmdResetPassword(args []string) error {
	fs := flag.NewFlagSet("reset-password", flag.ExitOnError)
	var cf commonFlags
	cf.register(fs)
	email := fs.String("email", "", "account email")
	_ = fs.Parse(args)
	if *email == "" {
		return errors.New("-email is required")
	}
	cfg, err := cf.load()
	if err != nil {
		return err
	}
	ctx := context.Background()
	st, _, err := app.OpenStore(ctx, cfg)
	if err != nil {
		return err
	}
	defer st.Close()
	u, err := st.GetUserByEmail(ctx, *email)
	if err != nil {
		return fmt.Errorf("user %s: %w", *email, err)
	}
	temp := secrets.RandomToken("", 12)
	u.PasswordHash = secrets.HashPassword(temp)
	u.MustChangePassword = true
	u.LockedUntil, u.FailedLogins, u.Disabled = 0, 0, false
	if err := st.UpdateUser(ctx, u); err != nil {
		return err
	}
	_ = st.DeleteUserSessions(ctx, u.ID)
	fmt.Printf("temporary password for %s: %s\n(you will be asked to change it after signing in)\n", u.Email, temp)
	return nil
}

// insecureLocalTransport is for probing this machine's own server over 127.0.0.1:
// the certificate is issued for the public name, so it can't be verified here.
func insecureLocalTransport() *http.Transport {
	return &http.Transport{TLSClientConfig: &tls.Config{InsecureSkipVerify: true}} //nolint:gosec
}

func cmdHealthcheck(args []string) error {
	fs := flag.NewFlagSet("healthcheck", flag.ExitOnError)
	url := fs.String("url", "", "health URL (default derived from GORGET_HTTP_LISTEN)")
	_ = fs.Parse(args)
	target := *url
	if target == "" {
		listen := os.Getenv("GORGET_HTTP_LISTEN")
		if listen == "" {
			listen = ":443"
		}
		scheme := "https"
		if os.Getenv("GORGET_TLS_MODE") == "off" {
			scheme = "http"
		}
		target = scheme + "://127.0.0.1" + listen[strings.LastIndex(listen, ":"):] + "/healthz"
	}
	client := &http.Client{Timeout: 5 * time.Second, Transport: insecureLocalTransport()}
	resp, err := client.Get(target)
	if err != nil {
		return err
	}
	resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("unhealthy: HTTP %d", resp.StatusCode)
	}
	return nil
}

const systemdUnit = `[Unit]
Description={{.Description}}
Documentation=https://github.com/anand34577/gorget
After=network-online.target
Wants=network-online.target

[Service]
ExecStart={{.Path|cmdEscape}}{{range .Arguments}} {{.|cmd}}{{end}}
Restart=on-failure
RestartSec=5
LimitNOFILE=65536
# Hardening: the gateway needs CAP_NET_ADMIN, binding :443/:80 needs CAP_NET_BIND_SERVICE.
AmbientCapabilities=CAP_NET_ADMIN CAP_NET_BIND_SERVICE CAP_NET_RAW
CapabilityBoundingSet=CAP_NET_ADMIN CAP_NET_BIND_SERVICE CAP_NET_RAW
NoNewPrivileges=true
ProtectSystem=strict
ProtectHome=true
PrivateTmp=true
ProtectKernelModules=true
ProtectControlGroups=true
ProtectClock=true
ProtectHostname=true
RestrictSUIDSGID=true
RestrictRealtime=true
LockPersonality=true
RestrictNamespaces=true
SystemCallArchitectures=native
StateDirectory=gorget
StateDirectoryMode=0700
ReadWritePaths=/var/lib/gorget /proc/sys/net
DeviceAllow=/dev/net/tun rw

[Install]
WantedBy=multi-user.target
`
