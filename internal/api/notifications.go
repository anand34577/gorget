package api

import (
	"bytes"
	"encoding/json"
	"io"
	"net/http"

	"github.com/anand34577/gorget/internal/core"
)

func (a *API) notificationsStatus(w http.ResponseWriter, r *http.Request) {
	if !who(r).can(permRead) {
		forbidden(w)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"settings": a.core.Settings().Redacted().Notifications,
		"status":   a.core.PushStatus(),
	})
}

func (a *API) putNotifications(w http.ResponseWriter, r *http.Request) {
	p := who(r)
	if !p.can(permManageSys) {
		forbidden(w)
		return
	}
	body, err := io.ReadAll(io.LimitReader(r.Body, 64<<10))
	if err != nil {
		badRequest(w, "cannot read body")
		return
	}
	var req struct {
		core.NotificationSettings
		// Tokens: absent keeps the stored one, "" removes it.
		GotifyToken *string `json:"gotify_token"`
		NtfyToken   *string `json:"ntfy_token"`
	}
	dec := json.NewDecoder(bytes.NewReader(body))
	dec.DisallowUnknownFields()
	if err := dec.Decode(&req); err != nil {
		badRequest(w, "invalid JSON: "+err.Error())
		return
	}
	saved, err := a.core.SaveNotificationSettings(r.Context(), core.NotificationUpdate{
		Settings: req.NotificationSettings, GotifyToken: req.GotifyToken, NtfyToken: req.NtfyToken,
	})
	if err != nil {
		a.fail(w, r, err)
		return
	}
	n := saved.Redacted().Notifications
	// The audit entry never contains a token.
	a.core.Audit(r.Context(), a.actor(r), "settings.update", "settings", "notifications", "notifications", map[string]any{
		"gotify": n.Gotify.Enabled, "ntfy": n.Ntfy.Enabled, "login_alerts": n.Login.Mode,
		"gotify_token_changed": req.GotifyToken != nil, "ntfy_token_changed": req.NtfyToken != nil,
	})
	a.core.Bus.Publish(core.EvSettingsUpdated, map[string]string{"section": "notifications"})
	writeJSON(w, http.StatusOK, n)
}

func (a *API) testNotification(w http.ResponseWriter, r *http.Request) {
	p := who(r)
	if !p.can(permManageSys) {
		forbidden(w)
		return
	}
	var req struct {
		Channel string `json:"channel"`
	}
	if !decode(w, r, &req) {
		return
	}
	if !a.loginRL.Allow("pushtest:" + p.user.ID) {
		writeErr(w, http.StatusTooManyRequests, "rate_limited", "wait a moment before sending another test")
		return
	}
	if err := a.core.SendTestPush(r.Context(), req.Channel); err != nil {
		if _, ok := err.(*core.InvalidError); ok {
			a.fail(w, r, err)
			return
		}
		writeErr(w, http.StatusBadGateway, "push_failed", err.Error())
		return
	}
	writeJSON(w, http.StatusOK, map[string]string{"sent": req.Channel})
}
