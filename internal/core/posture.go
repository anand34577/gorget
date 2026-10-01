package core

import (
	"fmt"
	"net/netip"
	"slices"
	"strconv"
	"strings"

	"github.com/anand34577/gorget/internal/store"
)

// Posture modes.
const (
	PostureEnforce = "enforce" // non-compliant devices lose network access
	PostureReport  = "report"  // non-compliant devices are only flagged in the console
)

// PostureSettings are the admin's device health rules. Clients report their state;
// a device that breaks a rule is blocked (enforce) or flagged (report).
type PostureSettings struct {
	Enabled bool   `json:"enabled"`
	Mode    string `json:"mode"`
	// MinClientVersion is a dotted version such as "0.3.0" ("" = any).
	MinClientVersion string `json:"min_client_version"`
	// MinOSVersion maps an OS (windows, darwin, linux, android) to its oldest allowed version.
	MinOSVersion          map[string]string `json:"min_os_version"`
	RequireDiskEncryption bool              `json:"require_disk_encryption"`
	RequireFirewall       bool              `json:"require_firewall"`
	// AllowedNetworks limits where devices may connect from (public address ranges):
	// a simple stand-in for location rules that needs no GeoIP database.
	AllowedNetworks []string `json:"allowed_networks"`
	// AllowedCountries limits connections to these countries (ISO codes such as "DE");
	// BlockedCountries refuses these. Countries come from the server's local country
	// database, looked up from the address the device connects from.
	AllowedCountries []string `json:"allowed_countries"`
	BlockedCountries []string `json:"blocked_countries"`
	// AllowedOS limits which operating systems may join (empty = all).
	AllowedOS []string `json:"allowed_os"`
	// GeoAutoUpdate downloads the free DB-IP country database monthly.
	GeoAutoUpdate bool `json:"geo_auto_update"`
	// ExemptTags skips all checks for tagged devices (servers, IoT).
	ExemptTags []string `json:"exempt_tags"`
}

func validCountry(c string) bool {
	if len(c) != 2 {
		return false
	}
	for _, r := range c {
		if r < 'A' || r > 'Z' {
			return false
		}
	}
	return true
}

// normalizePosture upper-cases country codes and lower-cases OS names.
func normalizePosture(p *PostureSettings) {
	up := func(in []string) []string {
		out := []string{}
		for _, c := range in {
			if c = strings.ToUpper(strings.TrimSpace(c)); c != "" && !slices.Contains(out, c) {
				out = append(out, c)
			}
		}
		return out
	}
	p.AllowedCountries, p.BlockedCountries = up(p.AllowedCountries), up(p.BlockedCountries)
	var oses []string
	for _, o := range p.AllowedOS {
		if o = strings.ToLower(strings.TrimSpace(o)); o != "" && !slices.Contains(oses, o) {
			oses = append(oses, o)
		}
	}
	if oses == nil {
		oses = []string{}
	}
	p.AllowedOS = oses
	if p.AllowedNetworks == nil {
		p.AllowedNetworks = []string{}
	}
	if p.ExemptTags == nil {
		p.ExemptTags = []string{}
	}
}

func validatePosture(p PostureSettings) error {
	switch p.Mode {
	case PostureEnforce, PostureReport:
	default:
		return fmt.Errorf("mode must be %q or %q", PostureEnforce, PostureReport)
	}
	if p.MinClientVersion != "" && !validVersion(p.MinClientVersion) {
		return fmt.Errorf("minimum client version %q must look like 1.2.3", p.MinClientVersion)
	}
	for os, v := range p.MinOSVersion {
		switch os {
		case "windows", "darwin", "linux", "android":
		default:
			return fmt.Errorf("unknown operating system %q (use windows, darwin, linux or android)", os)
		}
		if v != "" && !validVersion(v) {
			return fmt.Errorf("minimum %s version %q must look like 10.0.19045", os, v)
		}
	}
	for _, c := range append(append([]string{}, p.AllowedCountries...), p.BlockedCountries...) {
		if !validCountry(c) {
			return fmt.Errorf("%q is not a two-letter country code such as DE, US or IN", c)
		}
	}
	for _, c := range p.AllowedCountries {
		if slices.Contains(p.BlockedCountries, c) {
			return fmt.Errorf("%s is both allowed and blocked", c)
		}
	}
	for _, o := range p.AllowedOS {
		switch o {
		case "windows", "darwin", "linux", "android", "ios":
		default:
			return fmt.Errorf("unknown operating system %q (use windows, darwin, linux or android)", o)
		}
	}
	for _, n := range p.AllowedNetworks {
		if _, err := netip.ParsePrefix(n); err != nil {
			if _, err := netip.ParseAddr(n); err != nil {
				return fmt.Errorf("allowed network %q must be an address or a range like 203.0.113.0/24", n)
			}
		}
	}
	return nil
}

// PostureFailures lists the rules a device breaks (nil when compliant or checks are off).
// country is the device's country by public address ("" when unknown).
func PostureFailures(p PostureSettings, d *store.Device, country string) []string {
	if !p.Enabled || d.Kind != store.KindNative {
		return nil
	}
	for _, t := range d.Tags {
		if slices.Contains(p.ExemptTags, t) {
			return nil
		}
	}
	var out []string
	if p.MinClientVersion != "" && compareVersions(d.ClientVersion, p.MinClientVersion) < 0 {
		out = append(out, fmt.Sprintf("Gorget %s or newer is required (this device has %s)", p.MinClientVersion, orUnknown(d.ClientVersion)))
	}
	if min := p.MinOSVersion[d.OS]; min != "" {
		if have := extractVersion(d.OSVersion); have == "" || compareVersions(have, min) < 0 {
			out = append(out, fmt.Sprintf("%s %s or newer is required (this device reports %s)", osLabel(d.OS), min, orUnknown(d.OSVersion)))
		}
	}
	if p.RequireDiskEncryption && d.DiskEncrypted != 1 {
		if d.DiskEncrypted == 2 {
			out = append(out, "Disk encryption must be turned on")
		} else {
			out = append(out, "Disk encryption status is unknown; update Gorget and make sure encryption is on")
		}
	}
	if len(p.AllowedOS) > 0 && !slices.Contains(p.AllowedOS, d.OS) {
		out = append(out, fmt.Sprintf("%s devices aren't allowed on this network", osLabel(d.OS)))
	}
	if len(p.AllowedCountries) > 0 {
		switch {
		case country == "":
			out = append(out, fmt.Sprintf("The location of %s couldn't be determined, and this network only allows certain countries", orUnknown(d.PublicIP)))
		case !slices.Contains(p.AllowedCountries, country):
			out = append(out, fmt.Sprintf("Connecting from %s (%s) isn't allowed", country, d.PublicIP))
		}
	}
	if country != "" && slices.Contains(p.BlockedCountries, country) {
		out = append(out, fmt.Sprintf("Connecting from %s (%s) is blocked", country, d.PublicIP))
	}
	if p.RequireFirewall && d.FirewallOn != 1 {
		out = append(out, "The system firewall must be turned on")
	}
	if len(p.AllowedNetworks) > 0 {
		ip, err := netip.ParseAddr(d.PublicIP)
		allowed := false
		if err == nil {
			for _, n := range p.AllowedNetworks {
				if pf, e := netip.ParsePrefix(n); e == nil && pf.Contains(ip) {
					allowed = true
				} else if a, e := netip.ParseAddr(n); e == nil && a == ip {
					allowed = true
				}
			}
		}
		if !allowed {
			out = append(out, fmt.Sprintf("Connecting from %s isn't allowed", orUnknown(d.PublicIP)))
		}
	}
	return out
}

func orUnknown(s string) string {
	if s == "" {
		return "an unknown version"
	}
	return s
}

func osLabel(os string) string {
	switch os {
	case "darwin":
		return "macOS"
	case "windows":
		return "Windows"
	case "android":
		return "Android"
	}
	return "Linux"
}

func validVersion(v string) bool {
	if v == "" {
		return false
	}
	for _, part := range strings.Split(v, ".") {
		if _, err := strconv.Atoi(part); err != nil {
			return false
		}
	}
	return true
}

// extractVersion pulls the first dotted number out of strings such as
// "Windows 10.0 (build 26200)" or "macOS 14.5" or "Ubuntu 24.04.1 LTS".
func extractVersion(s string) string {
	for _, f := range strings.Fields(strings.NewReplacer("(", " ", ")", " ", ",", " ").Replace(s)) {
		f = strings.TrimPrefix(f, "v")
		if f != "" && f[0] >= '0' && f[0] <= '9' && validVersion(strings.TrimRight(f, ".")) {
			return strings.TrimRight(f, ".")
		}
	}
	return ""
}

// compareVersions compares dotted numeric versions; a leading "v" and any "-suffix"
// (such as "-dirty" or "-3-gabc") are ignored. Missing parts count as 0.
func compareVersions(a, b string) int {
	pa, pb := versionParts(a), versionParts(b)
	for i := 0; i < max(len(pa), len(pb)); i++ {
		var x, y int
		if i < len(pa) {
			x = pa[i]
		}
		if i < len(pb) {
			y = pb[i]
		}
		if x != y {
			if x < y {
				return -1
			}
			return 1
		}
	}
	return 0
}

func versionParts(v string) []int {
	v = strings.TrimPrefix(strings.TrimSpace(v), "v")
	if i := strings.IndexAny(v, "-+ "); i >= 0 {
		v = v[:i]
	}
	var out []int
	for _, p := range strings.Split(v, ".") {
		n, err := strconv.Atoi(p)
		if err != nil {
			break
		}
		out = append(out, n)
	}
	return out
}
