package api

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"slices"
	"sort"
	"strings"
	"time"

	"github.com/go-chi/chi/v5"

	"github.com/anand34577/gorget/internal/core"
	"github.com/anand34577/gorget/internal/secrets"
	"github.com/anand34577/gorget/internal/store"
)

// ---------- overview / dashboard ----------

func (a *API) overview(w http.ResponseWriter, r *http.Request) {
	p := who(r)
	s := a.core.Coord.Snapshot()
	var total, online, pending, native, wg, expiring, noncompliant int
	osCount := map[string]int{}
	soon := time.Now().Add(7 * 24 * time.Hour).Unix()
	for _, d := range s.Devices {
		if !canSeeDevice(p, d) || d.Kind == store.KindGateway {
			continue
		}
		total++
		if len(s.Posture[d.ID]) > 0 {
			noncompliant++
		}
		if a.core.DeviceOnline(d) {
			online++
		}
		if d.State == store.StatePending {
			pending++
		}
		switch d.Kind {
		case store.KindNative:
			native++
			osCount[d.OS]++
		case store.KindWireGuard:
			wg++
			osCount["wireguard"]++
		}
		if d.Kind == store.KindNative && !d.KeyExpiryDisabled && d.KeyExpiresAt > 0 && d.KeyExpiresAt < soon {
			expiring++
		}
	}
	pendingRoutes := 0
	for _, rs := range s.Routes {
		for _, rt := range rs {
			if rt.Advertised && !rt.Approved {
				pendingRoutes++
			}
		}
	}
	for _, d := range s.Devices {
		if d.ExitAdvertised && !d.ExitApproved {
			pendingRoutes++
		}
	}
	resp := map[string]any{
		"devices": map[string]int{
			"total": total, "online": online, "pending": pending, "native": native,
			"wireguard": wg, "expiring_soon": expiring, "non_compliant": noncompliant,
		},
		"os":             osCount,
		"pending_routes": pendingRoutes,
		"pending_access": a.pendingAccess(r, p),
		"users":          len(s.Users),
		"groups":         len(s.Groups),
		"policy_version": s.PolicyVer,
		"policy_error":   errString(s.PolicyErr),
		"network": map[string]any{
			"name": s.Settings.Network.Name, "ipv4": s.Settings.Network.IPv4, "ipv6": s.Settings.Network.IPv6,
			"domain": s.Settings.Network.Domain, "capacity": s.Plan.Capacity(),
		},
		"version": core.Version,
	}
	if p.can(permRead) {
		resp["gateway"] = a.gw.Status()
		resp["relay"] = a.sys.RelayStats()
		recent, _ := a.core.Store.ListAudit(r.Context(), store.AuditFilter{Limit: 8})
		if recent == nil {
			recent = []store.AuditEntry{}
		}
		resp["recent_activity"] = recent
	}
	writeJSON(w, http.StatusOK, resp)
}

func errString(err error) string {
	if err == nil {
		return ""
	}
	return err.Error()
}

// events streams change notifications to the console via Server-Sent Events.
func (a *API) events(w http.ResponseWriter, r *http.Request) {
	fl, ok := w.(http.Flusher)
	if !ok {
		writeErr(w, http.StatusInternalServerError, "internal", "streaming unsupported")
		return
	}
	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("Cache-Control", "no-store")
	w.Header().Set("X-Accel-Buffering", "no")
	w.WriteHeader(http.StatusOK)
	fl.Flush()
	ch, cancel := a.core.Bus.Subscribe(64)
	defer cancel()
	ping := time.NewTicker(20 * time.Second)
	defer ping.Stop()
	p := who(r)
	for {
		select {
		case <-r.Context().Done():
			return
		case <-ping.C:
			if _, err := fmt.Fprint(w, ": ping\n\n"); err != nil {
				return
			}
			fl.Flush()
		case ev, ok := <-ch:
			if !ok {
				return
			}
			// Non-admins only receive the event type (enough to trigger a refetch).
			payload := map[string]any{"type": ev.Type, "time": ev.Time}
			if p.can(permRead) {
				payload["data"] = ev.Data
			}
			b, _ := json.Marshal(payload)
			if _, err := fmt.Fprintf(w, "data: %s\n\n", b); err != nil {
				return
			}
			fl.Flush()
		}
	}
}

// networkMap returns a graph for the topology view.
func (a *API) networkMap(w http.ResponseWriter, r *http.Request) {
	p := who(r)
	s := a.core.Coord.Snapshot()
	type node struct {
		ID     string   `json:"id"`
		Name   string   `json:"name"`
		Kind   string   `json:"kind"`
		IPv4   string   `json:"ipv4"`
		OS     string   `json:"os"`
		Online bool     `json:"online"`
		Active bool     `json:"active"`
		Exit   bool     `json:"exit_node"`
		Routes []string `json:"routes"`
		User   string   `json:"user"`
		Tags   []string `json:"tags"`
		Relay  string   `json:"home_relay"`
	}
	type edge struct {
		Source string `json:"source"`
		Target string `json:"target"`
		Via    string `json:"via,omitempty"`
	}
	nodes := []node{}
	visible := map[string]bool{}
	for _, d := range s.Devices {
		if !canSeeDevice(p, d) && d.Kind != store.KindGateway {
			continue
		}
		visible[d.ID] = true
		n := node{ID: d.ID, Name: d.Name, Kind: d.Kind, IPv4: d.IPv4, OS: d.OS, Online: a.core.DeviceOnline(d), Active: s.Active[d.ID],
			Exit: d.ExitAdvertised && d.ExitApproved, User: s.OwnerEmail(d), Tags: d.Tags, Relay: d.HomeRelay, Routes: []string{}}
		for _, pfx := range s.PrimaryRoutesOf(d.ID) {
			n.Routes = append(n.Routes, pfx.String())
		}
		nodes = append(nodes, n)
	}
	edges := []edge{}
	seen := map[string]bool{}
	for id := range visible {
		if !s.Active[id] {
			continue
		}
		for _, peer := range s.Compiled.Peers(id) {
			if !visible[peer] || !s.Active[peer] {
				continue
			}
			a1, b1 := id, peer
			if a1 > b1 {
				a1, b1 = b1, a1
			}
			key := a1 + "|" + b1
			if seen[key] {
				continue
			}
			seen[key] = true
			e := edge{Source: a1, Target: b1}
			if s.Devices[a1].Kind == store.KindWireGuard || s.Devices[b1].Kind == store.KindWireGuard {
				e.Via = s.GatewayID
			}
			edges = append(edges, e)
		}
	}
	sort.Slice(nodes, func(i, j int) bool { return nodes[i].Name < nodes[j].Name })
	writeJSON(w, http.StatusOK, map[string]any{"nodes": nodes, "edges": edges, "gateway_id": s.GatewayID})
}

// ---------- setup keys ----------

func (a *API) listSetupKeys(w http.ResponseWriter, r *http.Request) {
	p := who(r)
	keys, err := a.core.Store.ListSetupKeys(r.Context())
	if err != nil {
		a.fail(w, r, err)
		return
	}
	out := []store.SetupKey{}
	for _, k := range keys {
		if p.can(permRead) || k.CreatedBy == p.user.ID {
			out = append(out, k)
		}
	}
	writeJSON(w, http.StatusOK, out)
}

func (a *API) createSetupKey(w http.ResponseWriter, r *http.Request) {
	p := who(r)
	var req struct {
		Name          string   `json:"name"`
		Reusable      bool     `json:"reusable"`
		Ephemeral     bool     `json:"ephemeral"`
		AutoApprove   *bool    `json:"auto_approve"`
		Tags          []string `json:"tags"`
		MaxUses       int      `json:"max_uses"`
		ExpiresInDays int      `json:"expires_in_days"`
	}
	if !decode(w, r, &req) {
		return
	}
	isAdmin := p.can(permManageNet)
	if !isAdmin && (!a.core.Settings().Auth.UserSetupKeys || !p.canWrite()) {
		forbidden(w)
		return
	}
	s := a.core.Coord.Snapshot()
	for _, t := range req.Tags {
		if !strings.HasPrefix(t, "tag:") || core.SanitizeName(strings.TrimPrefix(t, "tag:")) != strings.TrimPrefix(t, "tag:") {
			badRequest(w, fmt.Sprintf("tag %q must look like tag:<name>", t))
			return
		}
		if !isAdmin && !s.Policy.CanOwnTag(s.Env, p.user.ID, p.user.Email, t) {
			writeErr(w, http.StatusForbidden, "forbidden", fmt.Sprintf("you are not an owner of %s (see tagOwners in the policy)", t))
			return
		}
	}
	if strings.TrimSpace(req.Name) == "" {
		badRequest(w, "name is required")
		return
	}
	if req.ExpiresInDays < 0 || req.ExpiresInDays > 3650 || req.MaxUses < 0 {
		badRequest(w, "invalid expiry or max uses")
		return
	}
	auto := isAdmin
	if req.AutoApprove != nil && isAdmin {
		auto = *req.AutoApprove
	}
	plain := secrets.RandomToken("gsk_", 24)
	k := &store.SetupKey{
		ID: secrets.RandomID(), Name: strings.TrimSpace(req.Name), KeyHash: secrets.HashToken(plain), KeyPrefix: plain[:10],
		Reusable: req.Reusable, Ephemeral: req.Ephemeral, AutoApprove: auto, Tags: append(store.StringList{}, req.Tags...),
		MaxUses: req.MaxUses, CreatedBy: p.user.ID, CreatedAt: store.Now(),
	}
	if req.ExpiresInDays > 0 {
		k.ExpiresAt = time.Now().AddDate(0, 0, req.ExpiresInDays).Unix()
	}
	if err := a.core.Store.CreateSetupKey(r.Context(), k); err != nil {
		a.fail(w, r, err)
		return
	}
	a.core.Audit(r.Context(), a.actor(r), "setup_key.create", "setup_key", k.ID, k.Name, map[string]any{"reusable": k.Reusable, "ephemeral": k.Ephemeral, "tags": k.Tags})
	writeJSON(w, http.StatusCreated, map[string]any{"key": plain, "setup_key": k})
}

func (a *API) loadSetupKey(w http.ResponseWriter, r *http.Request) (*store.SetupKey, bool) {
	k, err := a.core.Store.GetSetupKey(r.Context(), chi.URLParam(r, "id"))
	if err != nil {
		a.fail(w, r, err)
		return nil, false
	}
	p := who(r)
	if !p.can(permManageNet) && k.CreatedBy != p.user.ID {
		forbidden(w)
		return nil, false
	}
	return k, true
}

func (a *API) revokeSetupKey(w http.ResponseWriter, r *http.Request) {
	k, ok := a.loadSetupKey(w, r)
	if !ok {
		return
	}
	k.Revoked = true
	if err := a.core.Store.UpdateSetupKey(r.Context(), k); err != nil {
		a.fail(w, r, err)
		return
	}
	a.core.Audit(r.Context(), a.actor(r), "setup_key.revoke", "setup_key", k.ID, k.Name, nil)
	writeJSON(w, http.StatusOK, k)
}

func (a *API) deleteSetupKey(w http.ResponseWriter, r *http.Request) {
	k, ok := a.loadSetupKey(w, r)
	if !ok {
		return
	}
	if err := a.core.Store.DeleteSetupKey(r.Context(), k.ID); err != nil {
		a.fail(w, r, err)
		return
	}
	a.core.Audit(r.Context(), a.actor(r), "setup_key.delete", "setup_key", k.ID, k.Name, nil)
	writeJSON(w, http.StatusOK, map[string]bool{"ok": true})
}

// ---------- SSO providers ----------

type oidcReq struct {
	Name            *string   `json:"name"`
	Issuer          *string   `json:"issuer"`
	ClientID        *string   `json:"client_id"`
	ClientSecret    *string   `json:"client_secret"`
	Scopes          *[]string `json:"scopes"`
	GroupsClaim     *string   `json:"groups_claim"`
	AllowedDomains  *[]string `json:"allowed_domains"`
	AutoCreateUsers *bool     `json:"auto_create_users"`
	DefaultRole     *string   `json:"default_role"`
	SyncGroups      *bool     `json:"sync_groups"`
	Enabled         *bool     `json:"enabled"`
}

func (a *API) applyOIDC(p *store.OIDCProvider, req oidcReq) error {
	if req.Name != nil {
		p.Name = strings.TrimSpace(*req.Name)
	}
	if req.Issuer != nil {
		u, err := url.Parse(strings.TrimSpace(*req.Issuer))
		if err != nil || u.Scheme != "https" && !(u.Scheme == "http" && (u.Hostname() == "localhost" || u.Hostname() == "127.0.0.1")) {
			return &core.InvalidError{Msg: "issuer must be an https URL"}
		}
		p.Issuer = strings.TrimRight(u.String(), "/")
	}
	if req.ClientID != nil {
		p.ClientID = strings.TrimSpace(*req.ClientID)
	}
	if req.ClientSecret != nil && *req.ClientSecret != "" {
		sealed, err := a.core.Box.Seal(*req.ClientSecret)
		if err != nil {
			return err
		}
		p.ClientSecret = sealed
	}
	if req.Scopes != nil {
		p.Scopes = *req.Scopes
	}
	if req.GroupsClaim != nil {
		p.GroupsClaim = *req.GroupsClaim
	}
	if req.AllowedDomains != nil {
		var ds store.StringList
		for _, d := range *req.AllowedDomains {
			if d = strings.ToLower(strings.TrimSpace(d)); d != "" {
				ds = append(ds, d)
			}
		}
		p.AllowedDomains = ds
	}
	if req.AutoCreateUsers != nil {
		p.AutoCreateUsers = *req.AutoCreateUsers
	}
	if req.DefaultRole != nil {
		if *req.DefaultRole == store.RoleOwner || !store.ValidRole(*req.DefaultRole) {
			return &core.InvalidError{Msg: "default role must be user, auditor, network_admin or admin"}
		}
		p.DefaultRole = *req.DefaultRole
	}
	if req.SyncGroups != nil {
		p.SyncGroups = *req.SyncGroups
	}
	if req.Enabled != nil {
		p.Enabled = *req.Enabled
	}
	if p.Name == "" || p.Issuer == "" || p.ClientID == "" {
		return &core.InvalidError{Msg: "name, issuer and client ID are required"}
	}
	if p.Scopes == nil {
		p.Scopes = store.StringList{}
	}
	if p.AllowedDomains == nil {
		p.AllowedDomains = store.StringList{}
	}
	return nil
}

func (a *API) listOIDC(w http.ResponseWriter, r *http.Request) {
	if !who(r).can(permRead) {
		forbidden(w)
		return
	}
	ps, err := a.core.Store.ListOIDCProviders(r.Context())
	if err != nil {
		a.fail(w, r, err)
		return
	}
	if ps == nil {
		ps = []store.OIDCProvider{}
	}
	writeJSON(w, http.StatusOK, map[string]any{"providers": ps, "redirect_url": a.core.Cfg.PublicURL + "/api/v1/auth/oidc/callback"})
}

func (a *API) createOIDC(w http.ResponseWriter, r *http.Request) {
	if !who(r).can(permManageSys) {
		forbidden(w)
		return
	}
	var req oidcReq
	if !decode(w, r, &req) {
		return
	}
	p := &store.OIDCProvider{ID: secrets.RandomID(), GroupsClaim: "groups", AutoCreateUsers: true, DefaultRole: store.RoleUser, SyncGroups: true, Enabled: true, CreatedAt: store.Now()}
	if err := a.applyOIDC(p, req); err != nil {
		a.fail(w, r, err)
		return
	}
	if err := a.core.Store.CreateOIDCProvider(r.Context(), p); err != nil {
		a.fail(w, r, err)
		return
	}
	a.core.Audit(r.Context(), a.actor(r), "sso.create", "sso_provider", p.ID, p.Name, map[string]string{"issuer": p.Issuer})
	writeJSON(w, http.StatusCreated, p)
}

func (a *API) updateOIDC(w http.ResponseWriter, r *http.Request) {
	if !who(r).can(permManageSys) {
		forbidden(w)
		return
	}
	var req oidcReq
	if !decode(w, r, &req) {
		return
	}
	p, err := a.core.Store.GetOIDCProvider(r.Context(), chi.URLParam(r, "id"))
	if err != nil {
		a.fail(w, r, err)
		return
	}
	if err := a.applyOIDC(p, req); err != nil {
		a.fail(w, r, err)
		return
	}
	if err := a.core.Store.UpdateOIDCProvider(r.Context(), p); err != nil {
		a.fail(w, r, err)
		return
	}
	a.core.Audit(r.Context(), a.actor(r), "sso.update", "sso_provider", p.ID, p.Name, nil)
	writeJSON(w, http.StatusOK, p)
}

func (a *API) deleteOIDC(w http.ResponseWriter, r *http.Request) {
	if !who(r).can(permManageSys) {
		forbidden(w)
		return
	}
	id := chi.URLParam(r, "id")
	if err := a.core.Store.DeleteOIDCProvider(r.Context(), id); err != nil {
		a.fail(w, r, err)
		return
	}
	a.core.Audit(r.Context(), a.actor(r), "sso.delete", "sso_provider", id, "", nil)
	writeJSON(w, http.StatusOK, map[string]bool{"ok": true})
}

func (a *API) testOIDC(w http.ResponseWriter, r *http.Request) {
	if !who(r).can(permManageSys) {
		forbidden(w)
		return
	}
	var req struct {
		Issuer string `json:"issuer"`
	}
	if !decode(w, r, &req) {
		return
	}
	if err := a.auth.TestOIDCProvider(r.Context(), req.Issuer); err != nil {
		writeJSON(w, http.StatusOK, map[string]any{"ok": false, "error": err.Error()})
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"ok": true})
}

// ---------- webhooks ----------

type webhookReq struct {
	Name    *string   `json:"name"`
	URL     *string   `json:"url"`
	Events  *[]string `json:"events"`
	Enabled *bool     `json:"enabled"`
}

func validWebhookURL(s string) bool {
	u, err := url.Parse(s)
	return err == nil && (u.Scheme == "https" || u.Scheme == "http") && u.Host != ""
}

func (a *API) listWebhooks(w http.ResponseWriter, r *http.Request) {
	if !who(r).can(permRead) {
		forbidden(w)
		return
	}
	hs, err := a.core.Store.ListWebhooks(r.Context())
	if err != nil {
		a.fail(w, r, err)
		return
	}
	if hs == nil {
		hs = []store.Webhook{}
	}
	writeJSON(w, http.StatusOK, map[string]any{"webhooks": hs, "events": core.WebhookEvents})
}

func (a *API) createWebhook(w http.ResponseWriter, r *http.Request) {
	if !who(r).can(permManageSys) {
		forbidden(w)
		return
	}
	var req webhookReq
	if !decode(w, r, &req) {
		return
	}
	if req.URL == nil || !validWebhookURL(*req.URL) {
		badRequest(w, "a valid http(s) URL is required")
		return
	}
	secret := secrets.RandomToken("whsec_", 24)
	sealed, err := a.core.Box.Seal(secret)
	if err != nil {
		a.fail(w, r, err)
		return
	}
	h := &store.Webhook{ID: secrets.RandomID(), URL: *req.URL, Secret: sealed, Events: store.StringList{}, Enabled: true, CreatedAt: store.Now()}
	if req.Name != nil {
		h.Name = *req.Name
	}
	if req.Events != nil {
		for _, e := range *req.Events {
			if e != "*" && !slices.Contains(core.WebhookEvents, e) {
				badRequest(w, "unknown event "+e)
				return
			}
		}
		h.Events = *req.Events
	}
	if err := a.core.Store.CreateWebhook(r.Context(), h); err != nil {
		a.fail(w, r, err)
		return
	}
	a.core.Audit(r.Context(), a.actor(r), "webhook.create", "webhook", h.ID, h.URL, nil)
	writeJSON(w, http.StatusCreated, map[string]any{"webhook": h, "secret": secret})
}

func (a *API) updateWebhook(w http.ResponseWriter, r *http.Request) {
	if !who(r).can(permManageSys) {
		forbidden(w)
		return
	}
	var req webhookReq
	if !decode(w, r, &req) {
		return
	}
	h, err := a.core.Store.GetWebhook(r.Context(), chi.URLParam(r, "id"))
	if err != nil {
		a.fail(w, r, err)
		return
	}
	if req.Name != nil {
		h.Name = *req.Name
	}
	if req.URL != nil {
		if !validWebhookURL(*req.URL) {
			badRequest(w, "a valid http(s) URL is required")
			return
		}
		h.URL = *req.URL
	}
	if req.Events != nil {
		h.Events = *req.Events
	}
	if req.Enabled != nil {
		h.Enabled = *req.Enabled
	}
	if err := a.core.Store.UpdateWebhook(r.Context(), h); err != nil {
		a.fail(w, r, err)
		return
	}
	a.core.Audit(r.Context(), a.actor(r), "webhook.update", "webhook", h.ID, h.URL, nil)
	writeJSON(w, http.StatusOK, h)
}

func (a *API) deleteWebhook(w http.ResponseWriter, r *http.Request) {
	if !who(r).can(permManageSys) {
		forbidden(w)
		return
	}
	id := chi.URLParam(r, "id")
	if err := a.core.Store.DeleteWebhook(r.Context(), id); err != nil {
		a.fail(w, r, err)
		return
	}
	a.core.Audit(r.Context(), a.actor(r), "webhook.delete", "webhook", id, "", nil)
	writeJSON(w, http.StatusOK, map[string]bool{"ok": true})
}

func (a *API) testWebhook(w http.ResponseWriter, r *http.Request) {
	if !who(r).can(permManageSys) {
		forbidden(w)
		return
	}
	h, err := a.core.Store.GetWebhook(r.Context(), chi.URLParam(r, "id"))
	if err != nil {
		a.fail(w, r, err)
		return
	}
	status, msg := a.core.TestWebhook(r.Context(), h)
	writeJSON(w, http.StatusOK, map[string]any{"status": status, "error": msg, "ok": status >= 200 && status < 300})
}

// ---------- audit & system ----------

func (a *API) listAudit(w http.ResponseWriter, r *http.Request) {
	p := who(r)
	q := r.URL.Query()
	f := store.AuditFilter{
		Action: q.Get("action"), TargetID: q.Get("target_id"), Search: q.Get("q"),
		Since: queryInt64(r, "since"), Until: queryInt64(r, "until"), BeforeSeq: queryInt64(r, "before"),
		Limit: queryInt(r, "limit", 100),
	}
	if !p.can(permRead) {
		f.ActorID = p.user.ID // users see their own activity
	} else if v := q.Get("actor_id"); v != "" {
		f.ActorID = v
	}
	entries, err := a.core.Store.ListAudit(r.Context(), f)
	if err != nil {
		a.fail(w, r, err)
		return
	}
	if entries == nil {
		entries = []store.AuditEntry{}
	}
	if q.Get("format") == "csv" && p.can(permRead) {
		w.Header().Set("Content-Type", "text/csv; charset=utf-8")
		w.Header().Set("Content-Disposition", `attachment; filename="gorget-audit.csv"`)
		fmt.Fprintln(w, "seq,time,actor,action,target_type,target,ip,details")
		for _, e := range entries {
			fmt.Fprintf(w, "%d,%s,%s,%s,%s,%s,%s,%s\n", e.Seq, time.Unix(e.TS, 0).UTC().Format(time.RFC3339),
				csv(e.Actor), csv(e.Action), csv(e.TargetType), csv(e.TargetName), csv(e.IP), csv(e.Details))
		}
		return
	}
	writeJSON(w, http.StatusOK, entries)
}

func csv(s string) string {
	if strings.ContainsAny(s, ",\"\n") {
		return `"` + strings.ReplaceAll(s, `"`, `""`) + `"`
	}
	if len(s) > 0 && strings.ContainsRune("=+-@", rune(s[0])) {
		return "'" + s // neutralise spreadsheet formula injection
	}
	return s
}

func (a *API) verifyAudit(w http.ResponseWriter, r *http.Request) {
	if !who(r).can(permRead) {
		forbidden(w)
		return
	}
	broken, n, err := a.core.Store.VerifyAuditChain(r.Context())
	if err != nil {
		a.fail(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"ok": broken == 0, "entries": n, "first_broken_seq": broken})
}

func (a *API) systemStatus(w http.ResponseWriter, r *http.Request) {
	if !who(r).can(permRead) {
		forbidden(w)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"version":         core.Version,
		"database":        a.core.Store.Driver(),
		"tls":             a.sys.TLSStatus(),
		"gateway":         a.gw.Status(),
		"relay":           a.sys.RelayStats(),
		"stun":            a.sys.STUNStats(),
		"online_devices":  a.core.Coord.OnlineCount(),
		"snapshot_serial": a.core.Coord.Snapshot().Serial,
		"cluster": map[string]any{
			"enabled":     a.core.Cfg.Cluster.Enabled,
			"instance_id": a.core.Cfg.Cluster.InstanceID,
			"leader":      a.core.IsLeader(),
			"instances":   a.clusterInstances(),
		},
		"udp_relay": a.core.Cfg.Relay.Enabled && a.core.Cfg.Relay.UDPListen != "",
	})
}

// pendingAccess counts access requests waiting for a decision (administrators only).
func (a *API) pendingAccess(r *http.Request, p *principal) int {
	if !p.can(permManageNet) {
		return 0
	}
	list, err := a.core.Store.ListAccessRequests(r.Context(), "", 500)
	if err != nil {
		return 0
	}
	n := 0
	for _, x := range list {
		if x.Status == store.AccessPending {
			n++
		}
	}
	return n
}

// clusterInstances lists the live server instances (empty when running alone).
func (a *API) clusterInstances() []core.ClusterInstance {
	if l := a.core.ClusterInstances(); l != nil {
		return l
	}
	return []core.ClusterInstance{}
}
