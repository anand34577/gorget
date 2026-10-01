package api

import (
	"bytes"
	"encoding/json"
	"io"
	"net/http"

	"github.com/go-chi/chi/v5"

	"github.com/anand34577/gorget/internal/core"
)

func (a *API) getSettings(w http.ResponseWriter, r *http.Request) {
	if !who(r).can(permRead) {
		forbidden(w)
		return
	}
	s := a.core.Coord.Snapshot()
	writeJSON(w, http.StatusOK, map[string]any{
		"settings": a.core.Settings().Redacted(),
		"derived": map[string]any{
			"gateway_ipv4": s.Plan.GatewayIPv4().String(),
			"gateway_ipv6": s.Plan.GatewayIPv6().String(),
			"capacity":     s.Plan.Capacity(),
			"devices":      len(s.Devices),
		},
		"server": map[string]any{
			"public_url":       a.core.Cfg.PublicURL,
			"tls_mode":         a.core.Cfg.TLS.Mode,
			"database":         a.core.Store.Driver(),
			"gateway_enabled":  a.core.Cfg.Gateway.Enabled,
			"gateway_endpoint": a.core.Cfg.Gateway.Endpoint,
			"relay_enabled":    a.core.Cfg.Relay.Enabled,
			"stun":             a.core.Cfg.STUN.Advertise,
			"version":          core.Version,
		},
	})
}

func (a *API) putSettings(w http.ResponseWriter, r *http.Request) {
	p := who(r)
	section := chi.URLParam(r, "section")
	switch section {
	case "dns", "devices", "client", "gateway", "network", "posture", "routing":
		if !p.can(permManageNet) {
			forbidden(w)
			return
		}
	case "auth":
		if !p.can(permManageSys) {
			forbidden(w)
			return
		}
	default:
		writeErr(w, http.StatusNotFound, "not_found", "unknown settings section")
		return
	}
	body, err := io.ReadAll(io.LimitReader(r.Body, 1<<20))
	if err != nil {
		badRequest(w, "cannot read body")
		return
	}
	strict := func(dst any) error {
		dec := json.NewDecoder(bytes.NewReader(body))
		dec.DisallowUnknownFields()
		return dec.Decode(dst)
	}
	saved, err := a.core.SaveSettings(r.Context(), section, func(s *core.AllSettings) error {
		var e error
		// Maps would merge with the stored ones; a PUT replaces the whole section.
		switch section {
		case "network":
			s.Network.Pools = nil
		case "posture":
			s.Posture.MinOSVersion = nil
		}
		switch section {
		case "dns":
			e = strict(&s.DNS)
		case "devices":
			e = strict(&s.Devices)
		case "client":
			e = strict(&s.Client)
		case "gateway":
			e = strict(&s.Gateway)
		case "network":
			e = strict(&s.Network)
		case "posture":
			e = strict(&s.Posture)
		case "routing":
			e = strict(&s.Routing)
		case "auth":
			e = strict(&s.Auth)
		}
		if e != nil {
			return &core.InvalidError{Msg: "invalid JSON: " + e.Error()}
		}
		return nil
	})
	if err != nil {
		if _, ok := err.(*core.InvalidError); ok {
			a.fail(w, r, err)
			return
		}
		badRequest(w, err.Error())
		return
	}
	a.core.Audit(r.Context(), a.actor(r), "settings.update", "settings", section, section, json.RawMessage(body))
	a.core.Bus.Publish(core.EvSettingsUpdated, map[string]string{"section": section})
	writeJSON(w, http.StatusOK, saved.Redacted())
}

func (a *API) readdress(w http.ResponseWriter, r *http.Request) {
	p := who(r)
	if !p.can(permManageNet) {
		forbidden(w)
		return
	}
	var req struct {
		IPv4   string `json:"ipv4"`
		IPv6   string `json:"ipv6"`
		DryRun bool   `json:"dry_run"`
	}
	if !decode(w, r, &req) {
		return
	}
	if !req.DryRun && !requireRecentAuth(w, p) {
		return
	}
	items, err := a.core.Readdress(r.Context(), a.actor(r), req.IPv4, req.IPv6, req.DryRun)
	if err != nil {
		a.fail(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"dry_run": req.DryRun, "devices": items})
}
