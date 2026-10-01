package api

import (
	"net/http"
	"net/netip"
	"sort"
	"strings"
	"time"

	"github.com/go-chi/chi/v5"

	"github.com/anand34577/gorget/internal/core"
	"github.com/anand34577/gorget/internal/policy"
	"github.com/anand34577/gorget/internal/store"
)

type deviceView struct {
	*store.Device
	Online     bool          `json:"online"`
	UserID     string        `json:"user_id"`
	UserEmail  string        `json:"user_email"`
	FQDN       string        `json:"fqdn"`
	Routes     []store.Route `json:"routes"`
	KeyExpired bool          `json:"key_expired"`
	HasPSK     bool          `json:"has_psk"`
	PeerCount  int           `json:"peer_count"`
	// Posture lists the security rules the device breaks (empty = compliant).
	Posture []string `json:"posture"`
	// RemoteIP is the public address the device connects from; Country its country code.
	RemoteIP string `json:"remote_ip"`
	Country  string `json:"country"`
}

func (a *API) view(s *core.Snapshot, d *store.Device) deviceView {
	v := deviceView{Device: d, Online: a.core.DeviceOnline(d), UserID: d.Owner(), UserEmail: s.OwnerEmail(d), FQDN: s.FQDN(d), Routes: s.Routes[d.ID], HasPSK: d.PSK != ""}
	if v.Routes == nil {
		v.Routes = []store.Route{}
	}
	if d.Kind == store.KindNative && !d.KeyExpiryDisabled && d.KeyExpiresAt > 0 && time.Now().Unix() >= d.KeyExpiresAt {
		v.KeyExpired = true
	}
	v.RemoteIP = core.RemoteIP(d)
	v.Country = a.core.Geo.Country(v.RemoteIP)
	v.Posture = s.Posture[d.ID]
	if v.Posture == nil {
		v.Posture = []string{}
	}
	if s.Active[d.ID] {
		v.PeerCount = len(s.Compiled.Peers(d.ID))
	}
	return v
}

// canSeeDevice: admins/auditors see all devices; users see their own.
func canSeeDevice(p *principal, d *store.Device) bool {
	return p.can(permRead) || d.Owner() == p.user.ID
}

func canManageDevice(p *principal, d *store.Device) bool {
	return p.can(permManageNet) || (d.Owner() == p.user.ID && p.canWrite() && d.Kind != store.KindGateway)
}

func (a *API) listDevices(w http.ResponseWriter, r *http.Request) {
	p := who(r)
	s := a.core.Coord.Snapshot()
	q := strings.ToLower(r.URL.Query().Get("q"))
	kind := r.URL.Query().Get("kind")
	out := []deviceView{}
	for _, d := range s.Devices {
		if !canSeeDevice(p, d) || (kind != "" && d.Kind != kind) {
			continue
		}
		if q != "" && !strings.Contains(strings.ToLower(d.Name+" "+d.Hostname+" "+d.IPv4+" "+s.OwnerEmail(d)+" "+strings.Join(d.Tags, " ")), q) {
			continue
		}
		out = append(out, a.view(s, d))
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Name < out[j].Name })
	writeJSON(w, http.StatusOK, out)
}

func (a *API) loadDevice(w http.ResponseWriter, r *http.Request) (*store.Device, bool) {
	d, err := a.core.Store.GetDevice(r.Context(), chi.URLParam(r, "id"))
	if err != nil {
		a.fail(w, r, err)
		return nil, false
	}
	if !canSeeDevice(who(r), d) {
		writeErr(w, http.StatusNotFound, "not_found", "not found")
		return nil, false
	}
	return d, true
}

func (a *API) getDevice(w http.ResponseWriter, r *http.Request) {
	d, ok := a.loadDevice(w, r)
	if !ok {
		return
	}
	writeJSON(w, http.StatusOK, a.view(a.core.Coord.Snapshot(), d))
}

type deviceUpdateReq struct {
	Name              *string   `json:"name"`
	Tags              *[]string `json:"tags"`
	IPv4              *string   `json:"ipv4"`
	KeyExpiryDisabled *bool     `json:"key_expiry_disabled"`
	ExitApproved      *bool     `json:"exit_approved"`
	State             *string   `json:"state"`
	ExpiresAt         *int64    `json:"expires_at"`
	TunnelMode        *string   `json:"tunnel_mode"`
	CustomAllowedIPs  *[]string `json:"custom_allowed_ips"`
}

func (a *API) updateDevice(w http.ResponseWriter, r *http.Request) {
	d, ok := a.loadDevice(w, r)
	if !ok {
		return
	}
	p := who(r)
	if !canManageDevice(p, d) {
		forbidden(w)
		return
	}
	var req deviceUpdateReq
	if !decode(w, r, &req) {
		return
	}
	if !p.can(permManageNet) {
		// Owners may rename their devices and change WireGuard config modes only.
		if req.Tags != nil || req.IPv4 != nil || req.KeyExpiryDisabled != nil || req.ExitApproved != nil || req.ExpiresAt != nil {
			forbidden(w)
			return
		}
		if req.State != nil && *req.State != store.StateDisabled && d.State == store.StateDisabled {
			forbidden(w)
			return
		}
	}
	if req.Tags != nil && !p.can(permManageNet) {
		forbidden(w)
		return
	}
	nd, err := a.core.UpdateDevice(r.Context(), a.actor(r), d.ID, core.DeviceUpdate{
		Name: req.Name, Tags: req.Tags, IPv4: req.IPv4, KeyExpiryDisabled: req.KeyExpiryDisabled,
		ExitApproved: req.ExitApproved, State: req.State, ExpiresAt: req.ExpiresAt,
		TunnelMode: req.TunnelMode, CustomAllowedIPs: req.CustomAllowedIPs,
	})
	if err != nil {
		a.fail(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, a.view(a.core.Coord.Snapshot(), nd))
}

func (a *API) deleteDevice(w http.ResponseWriter, r *http.Request) {
	d, ok := a.loadDevice(w, r)
	if !ok {
		return
	}
	if !canManageDevice(who(r), d) {
		forbidden(w)
		return
	}
	if err := a.core.DeleteDevice(r.Context(), a.actor(r), d.ID); err != nil {
		a.fail(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]bool{"ok": true})
}

func (a *API) approveDevice(w http.ResponseWriter, r *http.Request) {
	if !who(r).can(permManageNet) {
		forbidden(w)
		return
	}
	d, err := a.core.ApproveDevice(r.Context(), a.actor(r), chi.URLParam(r, "id"))
	if err != nil {
		a.fail(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, a.view(a.core.Coord.Snapshot(), d))
}

func (a *API) expireDeviceKey(w http.ResponseWriter, r *http.Request) {
	d, ok := a.loadDevice(w, r)
	if !ok {
		return
	}
	if !canManageDevice(who(r), d) {
		forbidden(w)
		return
	}
	if err := a.core.ExpireDeviceKey(r.Context(), a.actor(r), d.ID); err != nil {
		a.fail(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]bool{"ok": true})
}

// deviceAccess explains what a device can reach and who can reach it.
func (a *API) deviceAccess(w http.ResponseWriter, r *http.Request) {
	d, ok := a.loadDevice(w, r)
	if !ok {
		return
	}
	s := a.core.Coord.Snapshot()
	type peerInfo struct {
		ID   string `json:"id"`
		Name string `json:"name"`
		Kind string `json:"kind"`
		IPv4 string `json:"ipv4"`
	}
	type ruleInfo struct {
		RuleID string   `json:"rule_id"`
		Src    []string `json:"src"`
		Dst    []string `json:"dst"`
		Ports  string   `json:"ports"`
		Proto  string   `json:"proto"`
	}
	peers := []peerInfo{}
	if s.Active[d.ID] {
		for _, id := range s.Compiled.Peers(d.ID) {
			if pd := s.Devices[id]; pd != nil {
				peers = append(peers, peerInfo{ID: pd.ID, Name: pd.Name, Kind: pd.Kind, IPv4: pd.IPv4})
			}
		}
	}
	conv := func(rs []policy.FirewallRule) []ruleInfo {
		out := []ruleInfo{}
		for _, fr := range rs {
			out = append(out, ruleInfo{RuleID: fr.RuleID, Src: pfxStrings(fr.Src), Dst: pfxStrings(fr.Dst), Ports: portsString(fr.Ports), Proto: fr.Proto})
		}
		return out
	}
	resp := map[string]any{
		"active":         s.Active[d.ID],
		"peers":          peers,
		"inbound":        []ruleInfo{},
		"outbound":       []ruleInfo{},
		"can_use_exit":   false,
		"policy_version": s.PolicyVer,
	}
	if s.Active[d.ID] {
		resp["inbound"] = conv(s.Compiled.InboundRules(d.ID))
		resp["outbound"] = conv(s.Compiled.OutboundRules(d.ID))
		resp["can_use_exit"] = s.Compiled.CanUseExitNode(d.ID)
	}
	writeJSON(w, http.StatusOK, resp)
}

func pfxStrings(ps []netip.Prefix) []string {
	out := make([]string, 0, len(ps))
	for _, p := range ps {
		out = append(out, p.String())
	}
	return out
}

func portsString(ps []policy.PortRange) string {
	if len(ps) == 0 || (len(ps) == 1 && ps[0] == policy.AllPorts[0]) {
		return "*"
	}
	var parts []string
	for _, p := range ps {
		if p.First == p.Last {
			parts = append(parts, itoa(int(p.First)))
		} else {
			parts = append(parts, itoa(int(p.First))+"-"+itoa(int(p.Last)))
		}
	}
	return strings.Join(parts, ",")
}

// ---------- standard WireGuard configs ----------

type wgCreateReq struct {
	Name             string   `json:"name"`
	UserID           string   `json:"user_id"`
	PublicKey        string   `json:"public_key"`
	TunnelMode       string   `json:"tunnel_mode"`
	CustomAllowedIPs []string `json:"custom_allowed_ips"`
	ExpiresAt        int64    `json:"expires_at"`
	PresharedKey     *bool    `json:"preshared_key"`
	DNS              *bool    `json:"dns"`
	Tags             []string `json:"tags"`
	IPv4             string   `json:"ipv4"`
}

func (a *API) createWGConfig(w http.ResponseWriter, r *http.Request) {
	p := who(r)
	var req wgCreateReq
	if !decode(w, r, &req) {
		return
	}
	if !p.can(permManageNet) {
		if !a.core.Settings().Auth.UserWGConfigs || !p.canWrite() {
			forbidden(w)
			return
		}
		if (req.UserID != "" && req.UserID != p.user.ID) || len(req.Tags) > 0 || req.IPv4 != "" {
			forbidden(w)
			return
		}
		req.UserID = p.user.ID
	}
	if req.UserID == "" && len(req.Tags) == 0 {
		req.UserID = p.user.ID
	}
	if req.UserID != "" {
		if _, err := a.core.Store.GetUser(r.Context(), req.UserID); err != nil {
			badRequest(w, "unknown user")
			return
		}
	}
	psk, dns := true, true
	if req.PresharedKey != nil {
		psk = *req.PresharedKey
	}
	if req.DNS != nil {
		dns = *req.DNS
	}
	res, err := a.core.CreateWGConfig(r.Context(), a.actor(r), core.WGConfigParams{
		Name: req.Name, UserID: req.UserID, PublicKey: req.PublicKey, TunnelMode: req.TunnelMode,
		CustomAllowedIPs: req.CustomAllowedIPs, ExpiresAt: req.ExpiresAt, PresharedKey: psk, DNS: dns,
		Tags: req.Tags, IPv4: req.IPv4,
	})
	if err != nil {
		a.fail(w, r, err)
		return
	}
	writeJSON(w, http.StatusCreated, map[string]any{
		"device":      a.view(a.core.Coord.Snapshot(), res.Device),
		"private_key": res.PrivateKey,
		"config":      res.Config,
	})
}

func (a *API) wgConfig(w http.ResponseWriter, r *http.Request) {
	d, ok := a.loadDevice(w, r)
	if !ok {
		return
	}
	conf, err := a.core.RenderWGConfig(d, "")
	if err != nil {
		a.fail(w, r, err)
		return
	}
	if r.URL.Query().Get("download") == "1" {
		w.Header().Set("Content-Type", "text/plain; charset=utf-8")
		w.Header().Set("Content-Disposition", `attachment; filename="`+d.Name+`.conf"`)
		w.Header().Set("Cache-Control", "no-store")
		_, _ = w.Write([]byte(conf))
		return
	}
	writeJSON(w, http.StatusOK, map[string]string{"config": conf})
}

func (a *API) rotatePSK(w http.ResponseWriter, r *http.Request) {
	d, ok := a.loadDevice(w, r)
	if !ok {
		return
	}
	if !canManageDevice(who(r), d) {
		forbidden(w)
		return
	}
	nd, err := a.core.RotateWGPresharedKey(r.Context(), a.actor(r), d.ID)
	if err != nil {
		a.fail(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, a.view(a.core.Coord.Snapshot(), nd))
}

// ---------- interactive device login approval ----------

func (a *API) loadLogin(w http.ResponseWriter, r *http.Request) (*store.DeviceLogin, bool) {
	code := strings.ToUpper(strings.TrimSpace(chi.URLParam(r, "code")))
	if len(code) == 8 {
		code = code[:4] + "-" + code[4:]
	}
	if !a.loginRL.Allow("devlogin:" + who(r).user.ID) {
		writeErr(w, http.StatusTooManyRequests, "rate_limited", "too many attempts")
		return nil, false
	}
	l, err := a.core.Store.GetDeviceLoginByCode(r.Context(), code)
	if err != nil || l.Status != "pending" || store.Now() > l.ExpiresAt {
		writeErr(w, http.StatusNotFound, "not_found", "this code is invalid or has expired; start the login again on your device")
		return nil, false
	}
	return l, true
}

func (a *API) getDeviceLogin(w http.ResponseWriter, r *http.Request) {
	l, ok := a.loadLogin(w, r)
	if !ok {
		return
	}
	existing := ""
	if d, err := a.core.Store.GetDeviceByMachineKey(r.Context(), l.MachineKey); err == nil {
		existing = d.Name
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"code": l.UserCode, "hostname": l.Hostname, "os": l.OS, "created_at": l.CreatedAt, "expires_at": l.ExpiresAt,
		"existing_device":   existing,
		"approval_required": a.core.Settings().Devices.ApprovalRequired && !who(r).can(permManageNet),
	})
}

func (a *API) approveDeviceLogin(w http.ResponseWriter, r *http.Request) {
	p := who(r)
	if p.session == nil {
		writeErr(w, http.StatusForbidden, "forbidden", "device logins must be approved interactively")
		return
	}
	l, ok := a.loadLogin(w, r)
	if !ok {
		return
	}
	// A machine already owned by another user cannot be taken over by a non-admin.
	if d, err := a.core.Store.GetDeviceByMachineKey(r.Context(), l.MachineKey); err == nil && d.Owner() != "" && d.Owner() != p.user.ID && !p.can(permManageNet) {
		forbidden(w)
		return
	}
	l.Status, l.UserID = "approved", p.user.ID
	if err := a.core.Store.UpdateDeviceLogin(r.Context(), l); err != nil {
		a.fail(w, r, err)
		return
	}
	a.core.Audit(r.Context(), a.actor(r), "device.login_approve", "device_login", l.ID, l.Hostname, map[string]string{"os": l.OS})
	writeJSON(w, http.StatusOK, map[string]bool{"ok": true})
}

func (a *API) denyDeviceLogin(w http.ResponseWriter, r *http.Request) {
	l, ok := a.loadLogin(w, r)
	if !ok {
		return
	}
	l.Status = "denied"
	if err := a.core.Store.UpdateDeviceLogin(r.Context(), l); err != nil {
		a.fail(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]bool{"ok": true})
}

// ---------- routes ----------

type routeView struct {
	store.Route
	DeviceName string `json:"device_name"`
	Primary    bool   `json:"primary"`
	Online     bool   `json:"online"`
}

func (a *API) listRoutes(w http.ResponseWriter, r *http.Request) {
	p := who(r)
	s := a.core.Coord.Snapshot()
	out := []routeView{}
	for devID, rs := range s.Routes {
		d := s.Devices[devID]
		if d == nil || !canSeeDevice(p, d) {
			continue
		}
		for _, rt := range rs {
			pfx, _ := netip.ParsePrefix(rt.CIDR)
			out = append(out, routeView{Route: rt, DeviceName: d.Name, Primary: s.PrimaryRoute[pfx.Masked()] == devID, Online: a.core.DeviceOnline(d)})
		}
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].CIDR != out[j].CIDR {
			return out[i].CIDR < out[j].CIDR
		}
		return out[i].Priority < out[j].Priority
	})
	exits := []map[string]any{}
	for _, d := range s.Devices {
		if d.ExitAdvertised && canSeeDevice(p, d) {
			exits = append(exits, map[string]any{"device_id": d.ID, "device_name": d.Name, "approved": d.ExitApproved, "online": a.core.DeviceOnline(d), "kind": d.Kind})
		}
	}
	sort.Slice(exits, func(i, j int) bool { return exits[i]["device_name"].(string) < exits[j]["device_name"].(string) })
	writeJSON(w, http.StatusOK, map[string]any{"routes": out, "exit_nodes": exits})
}

func (a *API) updateRoute(w http.ResponseWriter, r *http.Request) {
	if !who(r).can(permManageNet) {
		forbidden(w)
		return
	}
	var req struct {
		Approved *bool `json:"approved"`
		Enabled  *bool `json:"enabled"`
		Priority *int  `json:"priority"`
	}
	if !decode(w, r, &req) {
		return
	}
	rt, err := a.core.Store.GetRoute(r.Context(), chi.URLParam(r, "id"))
	if err != nil {
		a.fail(w, r, err)
		return
	}
	if req.Approved != nil {
		rt.Approved = *req.Approved
	}
	if req.Enabled != nil {
		rt.Enabled = *req.Enabled
	}
	if req.Priority != nil {
		if *req.Priority < 0 || *req.Priority > 10000 {
			badRequest(w, "priority must be between 0 and 10000")
			return
		}
		rt.Priority = *req.Priority
	}
	if err := a.core.Store.UpdateRoute(r.Context(), rt); err != nil {
		a.fail(w, r, err)
		return
	}
	a.core.Audit(r.Context(), a.actor(r), "route.update", "route", rt.ID, rt.CIDR, req)
	a.core.Bus.Publish(core.EvRouteUpdated, rt)
	a.core.Coord.Trigger()
	writeJSON(w, http.StatusOK, rt)
}

func (a *API) deleteRoute(w http.ResponseWriter, r *http.Request) {
	if !who(r).can(permManageNet) {
		forbidden(w)
		return
	}
	rt, err := a.core.Store.GetRoute(r.Context(), chi.URLParam(r, "id"))
	if err != nil {
		a.fail(w, r, err)
		return
	}
	if err := a.core.Store.DeleteRoute(r.Context(), rt.ID); err != nil {
		a.fail(w, r, err)
		return
	}
	a.core.Audit(r.Context(), a.actor(r), "route.delete", "route", rt.ID, rt.CIDR, nil)
	a.core.Coord.Trigger()
	writeJSON(w, http.StatusOK, map[string]bool{"ok": true})
}

func itoa(n int) string {
	if n == 0 {
		return "0"
	}
	neg := n < 0
	if neg {
		n = -n
	}
	var b [20]byte
	i := len(b)
	for n > 0 {
		i--
		b[i] = byte('0' + n%10)
		n /= 10
	}
	if neg {
		i--
		b[i] = '-'
	}
	return string(b[i:])
}
