package core

import (
	"context"
	"errors"
	"fmt"
	"net"
	"net/netip"
	"net/url"
	"slices"
	"strconv"
	"strings"

	"github.com/anand34577/gorget/internal/ipam"
	"github.com/anand34577/gorget/internal/store"
)

// Settings keys.
const (
	keySetup   = "setup"
	keyNetwork = "network"
	keyDNS     = "dns"
	keyDevices = "devices"
	keyClient  = "client"
	keyAuth    = "auth"
	keyGateway = "gateway"
	keyPosture = "posture"
	keyRouting = "routing"
	// keyEmail is declared in email.go.
	keyGatewayKey = "gateway_key"
)

type SetupState struct {
	Completed   bool  `json:"completed"`
	CompletedAt int64 `json:"completed_at"`
}

type NetworkSettings struct {
	Name     string   `json:"name"`
	IPv4     string   `json:"ipv4"`
	IPv6     string   `json:"ipv6"`
	IPv6On   bool     `json:"ipv6_enabled"`
	Domain   string   `json:"domain"`
	Reserved []string `json:"reserved"`
	// Sub-pools: "tag:server" or "group:eng" -> CIDR inside the network.
	Pools map[string]string `json:"pools"`
}

type SplitDNS struct {
	Domain      string   `json:"domain"`
	Nameservers []string `json:"nameservers"`
}

type DNSRecord struct {
	Name  string `json:"name"`
	Type  string `json:"type"`
	Value string `json:"value"`
}

type DNSSettings struct {
	MagicDNS      bool        `json:"magic_dns"`
	Nameservers   []string    `json:"nameservers"`
	SearchDomains []string    `json:"search_domains"`
	Split         []SplitDNS  `json:"split"`
	Records       []DNSRecord `json:"records"`
	OverrideLocal bool        `json:"override_local"`
	// FailOpen falls back to system DNS when upstreams fail (false = fail closed).
	FailOpen bool `json:"fail_open"`
}

type DeviceSettings struct {
	ApprovalRequired     bool `json:"approval_required"`
	KeyExpiryDays        int  `json:"key_expiry_days"` // 0 = never
	EphemeralTimeoutMins int  `json:"ephemeral_timeout_mins"`
	InactiveCleanupDays  int  `json:"inactive_cleanup_days"` // 0 = never
}

type ClientSettings struct {
	AllowLANDefault     bool   `json:"allow_lan_default"`
	KillSwitchEnforced  bool   `json:"kill_switch_enforced"`
	ForcedExitNodeID    string `json:"forced_exit_node_id"`
	DefaultExitNodeID   string `json:"default_exit_node_id"`
	MTU                 int    `json:"mtu"`
	AllowUserExitChoice bool   `json:"allow_user_exit_choice"`
	AllowCustomDNS      bool   `json:"allow_custom_dns"`
}

type AuthSettings struct {
	PasswordLogin     bool `json:"password_login"`
	MFARequired       bool `json:"mfa_required"`
	MFARequiredAdmins bool `json:"mfa_required_admins"`
	// Users may create their own standard WireGuard configs.
	UserWGConfigs bool `json:"user_wg_configs"`
	// Users may create setup keys for their own devices.
	UserSetupKeys    bool `json:"user_setup_keys"`
	LockoutThreshold int  `json:"lockout_threshold"`
	LockoutMinutes   int  `json:"lockout_minutes"`
}

type GatewaySettings struct {
	// Offer the gateway as an exit node to native clients.
	ExitNode bool `json:"exit_node"`
	// Default tunnel mode for new standard WireGuard configs.
	DefaultTunnelMode string `json:"default_tunnel_mode"`
	// Routes included in split-tunnel configs in addition to the overlay.
	SplitRoutes []string `json:"split_routes"`
	// Default config lifetime in days (0 = no expiry).
	DefaultExpiryDays   int `json:"default_expiry_days"`
	PersistentKeepalive int `json:"persistent_keepalive"`
}

type gatewayKey struct {
	PrivateKey string `json:"private_key"` // sealed
}

// DomainRoute sends traffic to a domain through one device (an approved exit node), while all
// other traffic keeps its normal path. Clients resolve the names through Gorget DNS and
// route the answers on demand.
type DomainRoute struct {
	// Domain such as "example.com"; subdomains are included. A leading "*." is accepted.
	Domain string `json:"domain"`
	// Via is the name of the exit-node device that carries the traffic.
	Via string `json:"via"`
}

type RoutingSettings struct {
	DomainRoutes []DomainRoute `json:"domain_routes"`
}

// AllSettings is the full editable settings bundle.
type AllSettings struct {
	Network NetworkSettings `json:"network"`
	DNS     DNSSettings     `json:"dns"`
	Devices DeviceSettings  `json:"devices"`
	Client  ClientSettings  `json:"client"`
	Auth    AuthSettings    `json:"auth"`
	Gateway GatewaySettings `json:"gateway"`
	Posture PostureSettings `json:"posture"`
	Routing RoutingSettings `json:"routing"`
	Email   EmailSettings   `json:"email"`
	// Notifications: Gotify, ntfy and sign-in alerts.
	Notifications NotificationSettings `json:"notifications"`
}

func defaultSettings() AllSettings {
	return AllSettings{
		Network: NetworkSettings{
			Name:   "Gorget",
			IPv4:   ipam.DefaultIPv4,
			IPv6:   ipam.RandomULA().String(),
			IPv6On: true,
			Domain: "gorget.internal",
			Pools:  map[string]string{},
		},
		DNS: DNSSettings{
			MagicDNS:    true,
			Nameservers: []string{"https://cloudflare-dns.com/dns-query", "tls://9.9.9.9"},
			FailOpen:    true,
		},
		Devices: DeviceSettings{KeyExpiryDays: 180, EphemeralTimeoutMins: 30},
		Client: ClientSettings{
			AllowLANDefault:     true,
			MTU:                 1280,
			AllowUserExitChoice: true,
			AllowCustomDNS:      true,
		},
		Auth: AuthSettings{
			PasswordLogin:     true,
			MFARequiredAdmins: false,
			UserWGConfigs:     true,
			UserSetupKeys:     true,
			LockoutThreshold:  5,
			LockoutMinutes:    15,
		},
		Gateway: GatewaySettings{
			ExitNode:            true,
			DefaultTunnelMode:   store.TunnelSplit, // reach your devices and networks; full tunnel is a deliberate choice
			DefaultExpiryDays:   0,
			PersistentKeepalive: 25,
		},
		Posture: PostureSettings{Mode: PostureEnforce, MinOSVersion: map[string]string{}, AllowedNetworks: []string{}, ExemptTags: []string{}},
		Routing: RoutingSettings{DomainRoutes: []DomainRoute{}},
		Email:   defaultEmail(),

		Notifications: defaultNotifications(),
	}
}

func (c *Core) loadSettings(ctx context.Context) error {
	def := defaultSettings()
	s := def
	load := func(key string, dest any, fallback any) error {
		err := c.Store.GetSetting(ctx, key, dest)
		if errors.Is(err, store.ErrNotFound) {
			return c.Store.PutSetting(ctx, key, fallback)
		}
		return err
	}
	if err := load(keyNetwork, &s.Network, def.Network); err != nil {
		return err
	}
	if err := load(keyDNS, &s.DNS, def.DNS); err != nil {
		return err
	}
	if err := load(keyDevices, &s.Devices, def.Devices); err != nil {
		return err
	}
	if err := load(keyClient, &s.Client, def.Client); err != nil {
		return err
	}
	if err := load(keyAuth, &s.Auth, def.Auth); err != nil {
		return err
	}
	if err := load(keyGateway, &s.Gateway, def.Gateway); err != nil {
		return err
	}
	if err := load(keyPosture, &s.Posture, def.Posture); err != nil {
		return err
	}
	if err := load(keyRouting, &s.Routing, def.Routing); err != nil {
		return err
	}
	if err := load(keyEmail, &s.Email, def.Email); err != nil {
		return err
	}
	if err := load(keyNotify, &s.Notifications, def.Notifications); err != nil {
		return err
	}
	normalizePosture(&s.Posture)
	if s.Routing.DomainRoutes == nil {
		s.Routing.DomainRoutes = []DomainRoute{}
	}
	if s.Posture.MinOSVersion == nil {
		s.Posture.MinOSVersion = map[string]string{}
	}
	if s.Network.Pools == nil {
		s.Network.Pools = map[string]string{}
	}
	c.mu.Lock()
	c.settings = s
	c.mu.Unlock()
	return nil
}

// Settings returns a copy of the current settings.
func (c *Core) Settings() AllSettings {
	c.mu.RLock()
	defer c.mu.RUnlock()
	return c.settings
}

// Plan returns the current address plan.
func (c *Core) Plan() ipam.Plan {
	s := c.Settings().Network
	return planFrom(s)
}

func planFrom(s NetworkSettings) ipam.Plan {
	p := ipam.Plan{IPv4: netip.MustParsePrefix(s.IPv4), IPv6: netip.MustParsePrefix(s.IPv6)}
	for _, r := range s.Reserved {
		if pfx, err := parsePrefixOrAddr(r); err == nil {
			p.Reserved = append(p.Reserved, pfx)
		}
	}
	return p
}

func parsePrefixOrAddr(s string) (netip.Prefix, error) {
	if strings.Contains(s, "/") {
		p, err := netip.ParsePrefix(s)
		return p.Masked(), err
	}
	a, err := netip.ParseAddr(s)
	if err != nil {
		return netip.Prefix{}, err
	}
	return netip.PrefixFrom(a, a.BitLen()), nil
}

// ValidateNetwork checks network settings (range changes are handled by Readdress).
func ValidateNetwork(n NetworkSettings) error {
	var errs []error
	v4, err := netip.ParsePrefix(n.IPv4)
	if err != nil {
		errs = append(errs, fmt.Errorf("ipv4: %w", err))
	} else if err := ipam.ValidateIPv4Range(v4); err != nil {
		errs = append(errs, fmt.Errorf("ipv4: %w", err))
	}
	v6, err := netip.ParsePrefix(n.IPv6)
	if err != nil {
		errs = append(errs, fmt.Errorf("ipv6: %w", err))
	} else if err := ipam.ValidateIPv6Range(v6); err != nil {
		errs = append(errs, fmt.Errorf("ipv6: %w", err))
	}
	if !validDomain(n.Domain) {
		errs = append(errs, errors.New("domain must be a valid DNS name such as gorget.internal"))
	}
	for _, r := range n.Reserved {
		p, err := parsePrefixOrAddr(r)
		if err != nil || (v4.IsValid() && !v4.Overlaps(p)) {
			errs = append(errs, fmt.Errorf("reserved %q must be an address or range inside %s", r, n.IPv4))
		}
	}
	for sel, cidr := range n.Pools {
		if !strings.HasPrefix(sel, "tag:") && !strings.HasPrefix(sel, "group:") {
			errs = append(errs, fmt.Errorf("pool key %q must be tag:<name> or group:<name>", sel))
		}
		p, err := netip.ParsePrefix(cidr)
		if err != nil || !v4.IsValid() || p.Bits() < v4.Bits() || !v4.Contains(p.Addr()) {
			errs = append(errs, fmt.Errorf("pool %q (%s) must be a range inside %s", sel, cidr, n.IPv4))
		}
	}
	if strings.TrimSpace(n.Name) == "" {
		errs = append(errs, errors.New("network name is required"))
	}
	return errors.Join(errs...)
}

func validDomain(d string) bool {
	if d == "" || len(d) > 200 || strings.HasPrefix(d, ".") || strings.HasSuffix(d, ".") {
		return false
	}
	for _, label := range strings.Split(d, ".") {
		if !validLabel(label) {
			return false
		}
	}
	return strings.Contains(d, ".")
}

func validLabel(l string) bool {
	if l == "" || len(l) > 63 || l[0] == '-' || l[len(l)-1] == '-' {
		return false
	}
	for _, c := range l {
		if !(c >= 'a' && c <= 'z' || c >= '0' && c <= '9' || c == '-') {
			return false
		}
	}
	return true
}

// SaveSettings validates and persists one settings section.
func (c *Core) SaveSettings(ctx context.Context, section string, update func(*AllSettings) error) (AllSettings, error) {
	c.settingsMu.Lock()
	defer c.settingsMu.Unlock()
	s := c.Settings()
	old := s
	if err := update(&s); err != nil {
		return old, err
	}
	var key string
	var val any
	switch section {
	case keyNetwork:
		if s.Network.IPv4 != old.Network.IPv4 || s.Network.IPv6 != old.Network.IPv6 {
			return old, errors.New("changing the address range requires the re-addressing tool")
		}
		if err := ValidateNetwork(s.Network); err != nil {
			return old, err
		}
		key, val = keyNetwork, s.Network
	case keyDNS:
		normalizeDNS(&s.DNS)
		if err := validateDNS(s.DNS); err != nil {
			return old, err
		}
		key, val = keyDNS, s.DNS
	case keyDevices:
		if s.Devices.KeyExpiryDays < 0 || s.Devices.EphemeralTimeoutMins < 0 || s.Devices.InactiveCleanupDays < 0 {
			return old, errors.New("values must not be negative")
		}
		key, val = keyDevices, s.Devices
	case keyClient:
		if s.Client.MTU != 0 && (s.Client.MTU < 1280 || s.Client.MTU > 1500) {
			return old, errors.New("MTU must be between 1280 and 1500")
		}
		key, val = keyClient, s.Client
	case keyAuth:
		if s.Auth.LockoutThreshold < 0 || s.Auth.LockoutMinutes < 0 {
			return old, errors.New("values must not be negative")
		}
		key, val = keyAuth, s.Auth
	case keyGateway:
		switch s.Gateway.DefaultTunnelMode {
		case store.TunnelFull, store.TunnelSplit:
		default:
			return old, errors.New("default tunnel mode must be full or split")
		}
		for _, r := range s.Gateway.SplitRoutes {
			if _, err := netip.ParsePrefix(r); err != nil {
				return old, fmt.Errorf("split route %q: %w", r, err)
			}
		}
		key, val = keyGateway, s.Gateway
	case keyRouting:
		if s.Routing.DomainRoutes == nil {
			s.Routing.DomainRoutes = []DomainRoute{}
		}
		if err := c.validateRouting(s.Routing); err != nil {
			return old, err
		}
		key, val = keyRouting, s.Routing
	case keyPosture:
		if s.Posture.MinOSVersion == nil {
			s.Posture.MinOSVersion = map[string]string{}
		}
		normalizePosture(&s.Posture)
		if err := validatePosture(s.Posture); err != nil {
			return old, err
		}
		if s.Posture.Enabled && len(s.Posture.AllowedCountries)+len(s.Posture.BlockedCountries) > 0 && !c.Geo.Loaded() {
			return old, invalid("country rules need the country database: download it first (Device health > Country database)")
		}
		key, val = keyPosture, s.Posture
	case keyEmail:
		if err := validateEmail(s.Email); err != nil {
			return old, err
		}
		key, val = keyEmail, s.Email
	case keyNotify:
		if err := validateNotifications(s.Notifications); err != nil {
			return old, err
		}
		key, val = keyNotify, s.Notifications
	default:
		return old, fmt.Errorf("unknown settings section %q", section)
	}
	if err := c.Store.PutSetting(ctx, key, val); err != nil {
		return old, err
	}
	c.mu.Lock()
	c.settings = s
	c.mu.Unlock()
	if section == keyGateway {
		if err := c.syncGatewayDevice(ctx); err != nil {
			c.Log.Warn("gateway device sync failed", "err", err)
		}
	}
	c.Coord.Trigger()
	return s, nil
}

// normalizeDNS trims and lower-cases names so lookups (always lower-case) match them,
// and drops blank entries the console may send for empty rows.
func normalizeDNS(d *DNSSettings) {
	clean := func(v string) string { return strings.TrimSuffix(strings.ToLower(strings.TrimSpace(v)), ".") }
	var ns []string
	for _, n := range d.Nameservers {
		if n = strings.TrimSpace(n); n != "" && !slices.Contains(ns, n) {
			ns = append(ns, n)
		}
	}
	d.Nameservers = ns
	var search []string
	for _, sd := range d.SearchDomains {
		if sd = clean(sd); sd != "" && !slices.Contains(search, sd) {
			search = append(search, sd)
		}
	}
	d.SearchDomains = search
	for i := range d.Split {
		d.Split[i].Domain = clean(d.Split[i].Domain)
		var sns []string
		for _, n := range d.Split[i].Nameservers {
			if n = strings.TrimSpace(n); n != "" {
				sns = append(sns, n)
			}
		}
		d.Split[i].Nameservers = sns
	}
	for i := range d.Records {
		d.Records[i].Name = clean(d.Records[i].Name)
		d.Records[i].Type = strings.ToUpper(strings.TrimSpace(d.Records[i].Type))
		d.Records[i].Value = strings.TrimSpace(d.Records[i].Value)
	}
}

// validRecordName accepts a label ("files"), a name ("files.home.lan") or a wildcard
// over either ("*.apps", "*.apps.home.lan").
func validRecordName(n string) bool {
	n = strings.TrimPrefix(n, "*.")
	return validLabel(n) || validDomain(n)
}

func validateDNS(d DNSSettings) error {
	var errs []error
	if len(d.Nameservers) > 16 || len(d.Split) > 200 || len(d.Records) > 5000 {
		errs = append(errs, errors.New("too many entries (limits: 16 nameservers, 200 split domains, 5000 records)"))
	}
	for _, sd := range d.SearchDomains {
		if !validDomain(sd) && !validLabel(sd) {
			errs = append(errs, fmt.Errorf("search domain %q is invalid", sd))
		}
	}
	seenSplit := map[string]bool{}
	for _, sp := range d.Split {
		if seenSplit[sp.Domain] {
			errs = append(errs, fmt.Errorf("split DNS domain %q is listed twice", sp.Domain))
		}
		seenSplit[sp.Domain] = true
	}
	for _, ns := range d.Nameservers {
		if err := ValidateNameserver(ns); err != nil {
			errs = append(errs, err)
		}
	}
	for _, s := range d.Split {
		if !validDomain(strings.ToLower(s.Domain)) && !validLabel(strings.ToLower(s.Domain)) {
			errs = append(errs, fmt.Errorf("split DNS domain %q is invalid", s.Domain))
		}
		if len(s.Nameservers) == 0 {
			errs = append(errs, fmt.Errorf("split DNS %q needs at least one nameserver", s.Domain))
		}
		for _, ns := range s.Nameservers {
			if err := ValidateNameserver(ns); err != nil {
				errs = append(errs, err)
			}
		}
	}
	for _, r := range d.Records {
		switch strings.ToUpper(r.Type) {
		case "A":
			if a, err := netip.ParseAddr(r.Value); err != nil || !a.Is4() {
				errs = append(errs, fmt.Errorf("record %s: A value must be an IPv4 address", r.Name))
			}
		case "AAAA":
			if a, err := netip.ParseAddr(r.Value); err != nil || !a.Is6() {
				errs = append(errs, fmt.Errorf("record %s: AAAA value must be an IPv6 address", r.Name))
			}
		case "CNAME":
			if !validDomain(strings.TrimSuffix(strings.ToLower(r.Value), ".")) {
				errs = append(errs, fmt.Errorf("record %s: CNAME target must be a domain", r.Name))
			}
		default:
			errs = append(errs, fmt.Errorf("record %s: type must be A, AAAA or CNAME", r.Name))
		}
		if !validRecordName(strings.ToLower(r.Name)) {
			errs = append(errs, fmt.Errorf("record name %q is invalid (use a name like files, or *.apps for a wildcard)", r.Name))
		}
	}
	return errors.Join(errs...)
}

// ValidateNameserver accepts ip, ip:port, https://host/path (DoH) and tls://host[:port] (DoT).
func ValidateNameserver(ns string) error {
	switch {
	case strings.HasPrefix(ns, "https://"):
		u, err := url.Parse(ns)
		if err != nil || u.Hostname() == "" || u.User != nil {
			return fmt.Errorf("nameserver %q is not a valid DNS-over-HTTPS address like https://dns.example/dns-query", ns)
		}
		return nil
	case strings.HasPrefix(ns, "tls://"):
		hp := strings.TrimPrefix(ns, "tls://")
		host := hp
		if h, port, err := net.SplitHostPort(hp); err == nil {
			if p, err := strconv.Atoi(port); err != nil || p < 1 || p > 65535 {
				return fmt.Errorf("nameserver %q has an invalid port", ns)
			}
			host = h
		}
		if _, err := netip.ParseAddr(host); err != nil && !validDomain(strings.ToLower(host)) {
			return fmt.Errorf("nameserver %q is not a valid DNS-over-TLS address like tls://dns.example or tls://9.9.9.9", ns)
		}
		return nil
	}
	if _, err := netip.ParseAddr(ns); err == nil {
		return nil
	}
	if _, err := netip.ParseAddrPort(ns); err == nil {
		return nil
	}
	return fmt.Errorf("nameserver %q must be an IP, IP:port, https:// (DoH) or tls:// (DoT) address", ns)
}

// NormalizeRouteDomain lower-cases a domain and drops a leading "*." or ".".
func NormalizeRouteDomain(d string) string {
	d = strings.ToLower(strings.TrimSpace(d))
	d = strings.TrimPrefix(strings.TrimPrefix(d, "*"), ".")
	return strings.TrimSuffix(d, ".")
}

func (c *Core) validateRouting(r RoutingSettings) error {
	if len(r.DomainRoutes) > 200 {
		return errors.New("at most 200 domain routes are supported")
	}
	snap := c.Coord.Snapshot()
	seen := map[string]bool{}
	for _, dr := range r.DomainRoutes {
		d := NormalizeRouteDomain(dr.Domain)
		if !validDomain(d) {
			return fmt.Errorf("%q is not a domain such as example.com", dr.Domain)
		}
		if seen[d] {
			return fmt.Errorf("%s is listed twice", d)
		}
		seen[d] = true
		var via *store.Device
		for _, dev := range snap.Devices {
			if dev.Name == dr.Via {
				via = dev
			}
		}
		if via == nil || via.Kind != store.KindNative {
			return fmt.Errorf("%q is not a device that can carry this traffic", dr.Via)
		}
		if !(via.ExitAdvertised && via.ExitApproved) {
			return fmt.Errorf("%s must be an approved exit node first (Routes > Exit nodes)", dr.Via)
		}
	}
	return nil
}
