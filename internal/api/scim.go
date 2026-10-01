package api

import (
	"net/http"
)

// scimStatus tells administrators whether provisioning is on and where the IdP should connect.
func (a *API) scimStatus(w http.ResponseWriter, r *http.Request) {
	if !who(r).can(permManageSys) {
		forbidden(w)
		return
	}
	s := a.core.SCIM(r.Context())
	writeJSON(w, http.StatusOK, map[string]any{
		"enabled":    s.Enabled,
		"created_at": s.CreatedAt,
		"base_url":   a.core.Cfg.PublicURL + "/scim/v2",
	})
}

// scimRotate creates a new SCIM token (shown once), replacing any previous one.
func (a *API) scimRotate(w http.ResponseWriter, r *http.Request) {
	p := who(r)
	if !p.can(permManageSys) {
		forbidden(w)
		return
	}
	if !requireRecentAuth(w, p) {
		return
	}
	tok, err := a.core.RotateSCIMToken(r.Context())
	if err != nil {
		a.fail(w, r, err)
		return
	}
	a.core.Audit(r.Context(), a.actor(r), "scim.token", "settings", "scim", "scim", nil)
	writeJSON(w, http.StatusOK, map[string]any{"token": tok, "base_url": a.core.Cfg.PublicURL + "/scim/v2"})
}

func (a *API) scimDisable(w http.ResponseWriter, r *http.Request) {
	p := who(r)
	if !p.can(permManageSys) {
		forbidden(w)
		return
	}
	if err := a.core.DisableSCIM(r.Context()); err != nil {
		a.fail(w, r, err)
		return
	}
	a.core.Audit(r.Context(), a.actor(r), "scim.disable", "settings", "scim", "scim", nil)
	writeJSON(w, http.StatusOK, map[string]bool{"ok": true})
}
