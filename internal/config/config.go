// Package config loads gorget-server configuration from a YAML file,
// environment variables (GORGET_*) and command-line flags, in increasing
// order of precedence.
package config

import (
	"errors"
	"fmt"
	"net"
	"net/url"
	"os"
	"path/filepath"
	"reflect"
	"strconv"
	"strings"
	"time"

	"gopkg.in/yaml.v3"
)

type Config struct {
	// Public URL clients and browsers use to reach the server, e.g. https://vpn.example.com
	PublicURL string `yaml:"public_url" env:"PUBLIC_URL"`
	DataDir   string `yaml:"data_dir" env:"DATA_DIR"`
	LogLevel  string `yaml:"log_level" env:"LOG_LEVEL"`
	LogFormat string `yaml:"log_format" env:"LOG_FORMAT"` // text | json

	HTTP     HTTPConfig     `yaml:"http"`
	TLS      TLSConfig      `yaml:"tls"`
	Database DatabaseConfig `yaml:"database"`
	STUN     STUNConfig     `yaml:"stun"`
	Relay    RelayConfig    `yaml:"relay"`
	Gateway  GatewayConfig  `yaml:"gateway"`
	Metrics  MetricsConfig  `yaml:"metrics"`
	Security SecurityConfig `yaml:"security"`
	Backup   BackupConfig   `yaml:"backup"`
	Cluster  ClusterConfig  `yaml:"cluster"`
}

type HTTPConfig struct {
	// Address for HTTPS (or plain HTTP when TLS mode is "off").
	Listen string `yaml:"listen" env:"HTTP_LISTEN"`
	// Address for ACME HTTP-01 challenges and HTTP->HTTPS redirects. Empty disables.
	RedirectListen string `yaml:"redirect_listen" env:"HTTP_REDIRECT_LISTEN"`
	HTTP3          bool   `yaml:"http3" env:"HTTP3"`
	// Trusted reverse-proxy CIDRs whose X-Forwarded-For / X-Real-IP headers are honoured.
	TrustedProxies []string `yaml:"trusted_proxies" env:"TRUSTED_PROXIES"`
	AccessLog      bool     `yaml:"access_log" env:"ACCESS_LOG"`
	// Optional CIDR allow-list for the admin console and admin API.
	AdminAllowCIDRs []string `yaml:"admin_allow_cidrs" env:"ADMIN_ALLOW_CIDRS"`
}

// TLS modes.
const (
	TLSModeACME       = "acme"        // Let's Encrypt / ZeroSSL, HTTP-01 + TLS-ALPN-01
	TLSModeACMEDNS    = "acme-dns"    // ACME with DNS-01
	TLSModeCustom     = "custom"      // user-provided certificate files
	TLSModeInternalCA = "internal-ca" // self-managed local CA
	TLSModeOff        = "off"         // behind a TLS-terminating reverse proxy
)

type TLSConfig struct {
	Mode     string   `yaml:"mode" env:"TLS_MODE"`
	Domains  []string `yaml:"domains" env:"TLS_DOMAINS"`
	Email    string   `yaml:"email" env:"TLS_EMAIL"`
	Staging  bool     `yaml:"staging" env:"TLS_STAGING"`
	CertFile string   `yaml:"cert_file" env:"TLS_CERT_FILE"`
	KeyFile  string   `yaml:"key_file" env:"TLS_KEY_FILE"`
	// Fallback issuer when Let's Encrypt fails. "zerossl" or "" to disable.
	FallbackCA string `yaml:"fallback_ca" env:"TLS_FALLBACK_CA"`
	// ZeroSSL API key (free account) enabling the ZeroSSL fallback issuer.
	ZeroSSLAPIKey string `yaml:"zerossl_api_key" env:"TLS_ZEROSSL_API_KEY"`
	// External account binding for ACME CAs that require it (with acme_directory).
	EABKeyID  string `yaml:"eab_key_id" env:"TLS_EAB_KEY_ID"`
	EABMACKey string `yaml:"eab_mac_key" env:"TLS_EAB_MAC_KEY"`
	// Custom ACME directory URL (e.g. a private step-ca). Overrides Let's Encrypt.
	ACMEDirectory string `yaml:"acme_directory" env:"TLS_ACME_DIRECTORY"`
	// DNS-01 provider: cloudflare, digitalocean, hetzner, desec, route53, rfc2136.
	DNSProvider string            `yaml:"dns_provider" env:"TLS_DNS_PROVIDER"`
	DNSConfig   map[string]string `yaml:"dns_config"`
}

type DatabaseConfig struct {
	Driver string `yaml:"driver" env:"DB_DRIVER"` // sqlite | postgres
	// SQLite: file path (default <data_dir>/gorget.db). Postgres: connection URL.
	DSN          string `yaml:"dsn" env:"DB_DSN"`
	MaxOpenConns int    `yaml:"max_open_conns" env:"DB_MAX_OPEN_CONNS"`
}

type STUNConfig struct {
	Enabled bool   `yaml:"enabled" env:"STUN_ENABLED"`
	Listen  string `yaml:"listen" env:"STUN_LISTEN"`
	// Public host:port advertised to clients. Defaults to <public host>:3478.
	Advertise string `yaml:"advertise" env:"STUN_ADVERTISE"`
}

type RelayConfig struct {
	Enabled bool   `yaml:"enabled" env:"RELAY_ENABLED"`
	Region  string `yaml:"region" env:"RELAY_REGION"`
	// Additional external relays advertised to clients.
	External []ExternalRelay `yaml:"external"`
	// Per-connection forwarding limit in bytes/sec (0 = unlimited).
	RateLimitBytes int `yaml:"rate_limit_bytes" env:"RELAY_RATE_LIMIT_BYTES"`
	// UDPListen enables the UDP relay (faster than the WebSocket relay; clients fall back to
	// WebSocket when UDP is blocked), e.g. ":3479". Empty disables it.
	UDPListen string `yaml:"udp_listen" env:"RELAY_UDP_LISTEN"`
	// UDPAdvertise is the public host:port clients use for the UDP relay. Defaults to <public host>:<udp port>.
	UDPAdvertise string `yaml:"udp_advertise" env:"RELAY_UDP_ADVERTISE"`
}

// ClusterConfig runs several server instances against one PostgreSQL database.
// Put them behind a load balancer for the web console and the control API; every
// instance also runs its own relay, and clients use the nearest one.
type ClusterConfig struct {
	Enabled bool `yaml:"enabled" env:"CLUSTER_ENABLED"`
	// InstanceID identifies this process (default: the host name). Must be unique.
	InstanceID string `yaml:"instance_id" env:"CLUSTER_INSTANCE_ID"`
	// RelayURL is where clients reach this instance's relay, e.g. wss://eu.vpn.example.com/relay.
	// Defaults to <public_url>/relay.
	RelayURL string `yaml:"relay_url" env:"CLUSTER_RELAY_URL"`
	// PeerListen is the UDP address on which this instance receives relay packets forwarded by
	// the other instances (default ":3480"); PeerAddr is the host:port they use to reach it
	// (default: this machine's first non-loopback address). Keep this traffic on a private network.
	PeerListen string `yaml:"peer_listen" env:"CLUSTER_PEER_LISTEN"`
	PeerAddr   string `yaml:"peer_addr" env:"CLUSTER_PEER_ADDR"`
}

type ExternalRelay struct {
	Region string `yaml:"region"`
	Name   string `yaml:"name"`
	URL    string `yaml:"url"`
}

type GatewayConfig struct {
	Enabled bool `yaml:"enabled" env:"GATEWAY_ENABLED"`
	// WireGuard interface name.
	Interface  string `yaml:"interface" env:"GATEWAY_INTERFACE"`
	ListenPort int    `yaml:"listen_port" env:"GATEWAY_LISTEN_PORT"`
	// Public endpoint host:port written into client configs. Defaults to <public host>:<listen_port>.
	Endpoint string `yaml:"endpoint" env:"GATEWAY_ENDPOINT"`
	// Egress interface for NAT (exit traffic). Auto-detected when empty.
	EgressInterface string `yaml:"egress_interface" env:"GATEWAY_EGRESS_INTERFACE"`
	// Force userspace wireguard-go even when the kernel module exists.
	Userspace bool `yaml:"userspace" env:"GATEWAY_USERSPACE"`
	// Run the DNS resolver on the gateway's overlay address.
	DNS bool `yaml:"dns" env:"GATEWAY_DNS"`
	MTU int  `yaml:"mtu" env:"GATEWAY_MTU"`
}

type MetricsConfig struct {
	Enabled bool   `yaml:"enabled" env:"METRICS_ENABLED"`
	Listen  string `yaml:"listen" env:"METRICS_LISTEN"` // separate listener, e.g. 127.0.0.1:9090
	Token   string `yaml:"token" env:"METRICS_TOKEN"`
}

type SecurityConfig struct {
	// Master key for secrets at rest: base64 32 bytes. If empty, read from
	// MasterKeyFile (generated on first run).
	MasterKey     string        `yaml:"master_key" env:"MASTER_KEY"`
	MasterKeyFile string        `yaml:"master_key_file" env:"MASTER_KEY_FILE"`
	SessionTTL    time.Duration `yaml:"session_ttl" env:"SESSION_TTL"`
	// Only accept the admin console from the overlay network.
	AdminOnlyFromVPN bool `yaml:"admin_only_from_vpn" env:"ADMIN_ONLY_FROM_VPN"`
}

type BackupConfig struct {
	// Interval for automatic encrypted SQLite backups (0 = disabled).
	Interval time.Duration `yaml:"interval" env:"BACKUP_INTERVAL"`
	Keep     int           `yaml:"keep" env:"BACKUP_KEEP"`
	Dir      string        `yaml:"dir" env:"BACKUP_DIR"`
}

func Default() Config {
	return Config{
		DataDir:   defaultDataDir(),
		LogLevel:  "info",
		LogFormat: "text",
		HTTP: HTTPConfig{
			Listen:         ":443",
			RedirectListen: ":80",
			HTTP3:          true,
		},
		TLS: TLSConfig{
			Mode:       TLSModeACME,
			FallbackCA: "zerossl",
		},
		Database: DatabaseConfig{Driver: "sqlite", MaxOpenConns: 20},
		STUN:     STUNConfig{Enabled: true, Listen: ":3478"},
		Relay:    RelayConfig{Enabled: true, Region: "default", UDPListen: ":3479"},
		Gateway: GatewayConfig{
			Enabled:    true,
			Interface:  "gorget0",
			ListenPort: 51820,
			DNS:        true,
			MTU:        1420,
		},
		Metrics: MetricsConfig{Listen: "127.0.0.1:9090"},
		Security: SecurityConfig{
			SessionTTL: 12 * time.Hour,
		},
		Backup: BackupConfig{Interval: 24 * time.Hour, Keep: 7},
	}
}

func defaultDataDir() string {
	if v := os.Getenv("GORGET_DATA_DIR"); v != "" {
		return v
	}
	switch {
	case isWindows():
		if pd := os.Getenv("ProgramData"); pd != "" {
			return filepath.Join(pd, "Gorget")
		}
		return `C:\ProgramData\Gorget`
	default:
		return "/var/lib/gorget"
	}
}

// Load reads the YAML file (if path is non-empty and exists) and applies env overrides.
func Load(path string) (Config, error) {
	cfg := Default()
	if path != "" {
		b, err := os.ReadFile(path)
		switch {
		case err == nil:
			if err := yaml.Unmarshal(b, &cfg); err != nil {
				return cfg, fmt.Errorf("parse %s: %w", path, err)
			}
		case errors.Is(err, os.ErrNotExist):
			return cfg, fmt.Errorf("config file %s not found", path)
		default:
			return cfg, err
		}
	}
	if err := applyEnv(reflect.ValueOf(&cfg).Elem()); err != nil {
		return cfg, err
	}
	return cfg, nil
}

// Finalize fills derived defaults and validates. Call after flags are applied.
func (c *Config) Finalize() error {
	var errs []error
	if c.PublicURL == "" {
		errs = append(errs, errors.New("public_url is required (e.g. https://vpn.example.com)"))
	} else {
		u, err := url.Parse(c.PublicURL)
		if err != nil || u.Host == "" || (u.Scheme != "https" && u.Scheme != "http") {
			errs = append(errs, fmt.Errorf("public_url %q must be an absolute http(s) URL", c.PublicURL))
		}
		c.PublicURL = strings.TrimRight(c.PublicURL, "/")
	}
	if c.DataDir == "" {
		errs = append(errs, errors.New("data_dir is required"))
	}
	host := c.PublicHost()

	switch c.TLS.Mode {
	case TLSModeACME, TLSModeACMEDNS, TLSModeInternalCA:
		if len(c.TLS.Domains) == 0 && host != "" {
			c.TLS.Domains = []string{host}
		}
	case TLSModeCustom:
		if c.TLS.CertFile == "" || c.TLS.KeyFile == "" {
			errs = append(errs, errors.New("tls.cert_file and tls.key_file are required in custom mode"))
		}
	case TLSModeOff:
	default:
		errs = append(errs, fmt.Errorf("tls.mode %q is invalid (acme, acme-dns, custom, internal-ca, off)", c.TLS.Mode))
	}
	if c.TLS.Mode == TLSModeACMEDNS && c.TLS.DNSProvider == "" {
		errs = append(errs, errors.New("tls.dns_provider is required in acme-dns mode"))
	}
	if (c.TLS.Mode == TLSModeACME || c.TLS.Mode == TLSModeACMEDNS) && net.ParseIP(host) != nil {
		errs = append(errs, errors.New("ACME certificates need a domain name in public_url, not an IP address; use tls.mode internal-ca or custom"))
	}

	switch c.Database.Driver {
	case "sqlite":
		if c.Database.DSN == "" {
			c.Database.DSN = filepath.Join(c.DataDir, "gorget.db")
		}
	case "postgres":
		if c.Database.DSN == "" {
			errs = append(errs, errors.New("database.dsn is required for postgres"))
		}
	default:
		errs = append(errs, fmt.Errorf("database.driver %q is invalid (sqlite, postgres)", c.Database.Driver))
	}

	if c.Relay.UDPListen != "" && c.Relay.UDPAdvertise == "" {
		_, port, _ := net.SplitHostPort(c.Relay.UDPListen)
		c.Relay.UDPAdvertise = net.JoinHostPort(host, port)
	}
	if c.Cluster.Enabled {
		if c.Database.Driver != "postgres" {
			errs = append(errs, errors.New("cluster.enabled needs database.driver postgres (instances share one database)"))
		}
		if c.TLS.Mode == TLSModeInternalCA {
			errs = append(errs, errors.New("cluster.enabled can't use tls.mode internal-ca (each instance would create its own CA); use acme, acme-dns, custom or off"))
		}
		if c.Cluster.InstanceID == "" {
			h, _ := os.Hostname()
			c.Cluster.InstanceID = h
		}
		if c.Cluster.InstanceID == "" {
			errs = append(errs, errors.New("cluster.instance_id is required"))
		}
		if c.Cluster.PeerListen == "" {
			c.Cluster.PeerListen = ":3480"
		}
		if c.Cluster.PeerAddr == "" {
			c.Cluster.PeerAddr = guessPeerAddr(c.Cluster.PeerListen)
		}
		if c.Cluster.RelayURL == "" && c.PublicURL != "" {
			scheme := "wss"
			if strings.HasPrefix(c.PublicURL, "http://") {
				scheme = "ws"
			}
			c.Cluster.RelayURL = scheme + "://" + strings.TrimPrefix(strings.TrimPrefix(c.PublicURL, "https://"), "http://") + "/relay"
		}
	}
	if c.STUN.Advertise == "" && c.STUN.Enabled {
		_, port, _ := net.SplitHostPort(c.STUN.Listen)
		c.STUN.Advertise = net.JoinHostPort(host, port)
	}
	if c.Gateway.Endpoint == "" {
		c.Gateway.Endpoint = net.JoinHostPort(host, strconv.Itoa(c.Gateway.ListenPort))
	}
	if c.Gateway.MTU == 0 {
		c.Gateway.MTU = 1420
	}
	if c.Security.MasterKeyFile == "" {
		c.Security.MasterKeyFile = filepath.Join(c.DataDir, "master.key")
	}
	if c.Security.SessionTTL <= 0 {
		c.Security.SessionTTL = 12 * time.Hour
	}
	if c.Backup.Dir == "" {
		c.Backup.Dir = filepath.Join(c.DataDir, "backups")
	}
	if c.Backup.Keep <= 0 {
		c.Backup.Keep = 7
	}
	if c.Metrics.Enabled && c.Metrics.Token == "" && !isLoopback(c.Metrics.Listen) {
		errs = append(errs, errors.New("metrics.token is required when metrics listen on a non-loopback address"))
	}
	for _, cidr := range append(append([]string{}, c.HTTP.TrustedProxies...), c.HTTP.AdminAllowCIDRs...) {
		if _, _, err := net.ParseCIDR(cidr); err != nil {
			errs = append(errs, fmt.Errorf("invalid CIDR %q", cidr))
		}
	}
	return errors.Join(errs...)
}

// PublicHost is the hostname (without port) of PublicURL.
func (c *Config) PublicHost() string {
	u, err := url.Parse(c.PublicURL)
	if err != nil {
		return ""
	}
	return u.Hostname()
}

func isLoopback(addr string) bool {
	h, _, err := net.SplitHostPort(addr)
	if err != nil {
		return false
	}
	if h == "localhost" {
		return true
	}
	ip := net.ParseIP(h)
	return ip != nil && ip.IsLoopback()
}

// applyEnv walks the struct and applies GORGET_<env tag> variables.
func applyEnv(v reflect.Value) error {
	t := v.Type()
	for i := 0; i < t.NumField(); i++ {
		f := t.Field(i)
		fv := v.Field(i)
		if f.Type.Kind() == reflect.Struct && f.Type != reflect.TypeOf(time.Duration(0)) {
			if err := applyEnv(fv); err != nil {
				return err
			}
			continue
		}
		tag := f.Tag.Get("env")
		if tag == "" {
			continue
		}
		raw, ok := os.LookupEnv("GORGET_" + tag)
		if !ok {
			continue
		}
		if err := setField(fv, raw); err != nil {
			return fmt.Errorf("GORGET_%s: %w", tag, err)
		}
	}
	return nil
}

func setField(fv reflect.Value, raw string) error {
	switch fv.Interface().(type) {
	case time.Duration:
		d, err := time.ParseDuration(raw)
		if err != nil {
			return err
		}
		fv.Set(reflect.ValueOf(d))
		return nil
	}
	switch fv.Kind() {
	case reflect.String:
		fv.SetString(raw)
	case reflect.Bool:
		b, err := strconv.ParseBool(raw)
		if err != nil {
			return err
		}
		fv.SetBool(b)
	case reflect.Int:
		n, err := strconv.Atoi(raw)
		if err != nil {
			return err
		}
		fv.SetInt(int64(n))
	case reflect.Slice:
		var out []string
		for _, s := range strings.Split(raw, ",") {
			if s = strings.TrimSpace(s); s != "" {
				out = append(out, s)
			}
		}
		fv.Set(reflect.ValueOf(out))
	default:
		return fmt.Errorf("unsupported type %s", fv.Kind())
	}
	return nil
}

// Example returns an annotated example configuration file.
func Example() string { return exampleYAML }

// guessPeerAddr returns this machine's first private (or otherwise non-loopback) address with the listen port.
func guessPeerAddr(listen string) string {
	_, port, err := net.SplitHostPort(listen)
	if err != nil {
		return ""
	}
	addrs, _ := net.InterfaceAddrs()
	var fallback string
	for _, a := range addrs {
		ipn, ok := a.(*net.IPNet)
		if !ok || ipn.IP.IsLoopback() || ipn.IP.IsLinkLocalUnicast() || ipn.IP.To4() == nil {
			continue
		}
		if ipn.IP.IsPrivate() {
			return net.JoinHostPort(ipn.IP.String(), port)
		}
		if fallback == "" {
			fallback = net.JoinHostPort(ipn.IP.String(), port)
		}
	}
	return fallback
}
