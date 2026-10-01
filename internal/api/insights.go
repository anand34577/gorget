package api

import (
	"bytes"
	"encoding/json"
	"io"
	"net/http"
	"net/mail"
	"sort"
	"strings"
	"time"

	"github.com/anand34577/gorget/internal/auth"
	"github.com/anand34577/gorget/internal/core"
	"github.com/anand34577/gorget/internal/geoip"
	"github.com/anand34577/gorget/internal/secrets"
	"github.com/anand34577/gorget/internal/store"
)

// ---------- statistics ----------

var statRanges = map[string]time.Duration{
	"24h": 24 * time.Hour,
	"7d":  7 * 24 * time.Hour,
	"30d": 30 * 24 * time.Hour,
	"90d": 90 * 24 * time.Hour,
}

type trafficRow struct {
	ID     string `json:"id"`
	Name   string `json:"name"`
	Kind   string `json:"kind"`
	Online bool   `json:"online"`
	Rx     int64  `json:"rx"`
	Tx     int64  `json:"tx"`
}

type sessionView struct {
	store.DeviceSession
	DeviceName string `json:"device_name"`
}

// stats returns the data behind the Insights page.
func (a *API) stats(w http.ResponseWriter, r *http.Request) {
	p := who(r)
	if !p.can(permRead) {
		forbidden(w)
		return
	}
	rng := r.URL.Query().Get("range")
	d, ok := statRanges[rng]
	if !ok {
		rng, d = "24h", statRanges["24h"]
	}
	ctx := r.Context()
	since := time.Now().Add(-d)
	series, err := a.core.StatsSeries(ctx, since, 144)
	if err != nil {
		a.fail(w, r, err)
		return
	}
	devs, err := a.core.Store.ListDevices(ctx)
	if err != nil {
		a.fail(w, r, err)
		return
	}
	snap := a.core.Coord.Snapshot()
	var top []trafficRow
	online, total := 0, 0
	for i := range devs {
		dv := &devs[i]
		if dv.Kind == store.KindGateway {
			continue
		}
		total++
		on := a.core.DeviceOnline(dv)
		if on {
			online++
		}
		if dv.RxBytes+dv.TxBytes > 0 {
			top = append(top, trafficRow{ID: dv.ID, Name: dv.Name, Kind: dv.Kind, Online: on, Rx: dv.RxBytes, Tx: dv.TxBytes})
		}
	}
	sort.Slice(top, func(i, j int) bool { return top[i].Rx+top[i].Tx > top[j].Rx+top[j].Tx })
	if len(top) > 10 {
		top = top[:10]
	}
	if top == nil {
		top = []trafficRow{}
	}
	sess, err := a.core.Store.RecentSessions(ctx, since.Unix(), 200)
	if err != nil {
		a.fail(w, r, err)
		return
	}
	views := make([]sessionView, 0, len(sess))
	for _, s := range sess {
		name := s.DeviceID
		if dv := snap.Devices[s.DeviceID]; dv != nil {
			name = dv.Name
		}
		views = append(views, sessionView{DeviceSession: s, DeviceName: name})
	}
	var routes, routesUp int
	for _, via := range snap.PrimaryRoute {
		routes++
		if dv := snap.Devices[via]; dv != nil && a.core.DeviceOnline(dv) {
			routesUp++
		}
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"range":     rng,
		"series":    series,
		"breakdown": a.core.Breakdown(devs),
		"top":       top,
		"sessions":  views,
		"current": map[string]any{
			"online": online, "total": total, "blocked": len(snap.Posture),
			"routes": routes, "routes_up": routesUp,
		},
		"geo": map[string]any{"loaded": a.core.Geo.Loaded(), "attribution": a.core.Geo.Status().Source},
	})
}

// deviceSessions lists when a device was connected and from where.
func (a *API) deviceSessions(w http.ResponseWriter, r *http.Request) {
	d, ok := a.loadDevice(w, r)
	if !ok {
		return
	}
	out, err := a.core.Store.DeviceSessions(r.Context(), d.ID, 100)
	if err != nil {
		a.fail(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, out)
}

// ---------- site routes ----------

func (a *API) createRoute(w http.ResponseWriter, r *http.Request) {
	if !who(r).can(permManageNet) {
		forbidden(w)
		return
	}
	var req struct {
		DeviceID string `json:"device_id"`
		CIDR     string `json:"cidr"`
	}
	if !decode(w, r, &req) {
		return
	}
	rt, err := a.core.AddSiteRoute(r.Context(), a.actor(r), req.DeviceID, req.CIDR)
	if err != nil {
		a.fail(w, r, err)
		return
	}
	writeJSON(w, http.StatusCreated, rt)
}

// ---------- email ----------

func (a *API) emailStatus(w http.ResponseWriter, r *http.Request) {
	if !who(r).can(permRead) {
		forbidden(w)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"settings": a.core.Settings().Redacted().Email,
		"status":   a.core.MailStatus(),
	})
}

func (a *API) putEmail(w http.ResponseWriter, r *http.Request) {
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
		core.EmailSettings
		// Password: absent keeps the stored one, "" removes it.
		Password *string `json:"password"`
	}
	dec := json.NewDecoder(bytes.NewReader(body))
	dec.DisallowUnknownFields()
	if err := dec.Decode(&req); err != nil {
		badRequest(w, "invalid JSON: "+err.Error())
		return
	}
	saved, err := a.core.SaveEmailSettings(r.Context(), core.EmailUpdate{Settings: req.EmailSettings, Password: req.Password})
	if err != nil {
		a.fail(w, r, err)
		return
	}
	// The audit entry never contains the password.
	e := saved.Redacted().Email
	a.core.Audit(r.Context(), a.actor(r), "settings.update", "settings", "email", "email", map[string]any{
		"enabled": e.Enabled, "host": e.Host, "port": e.Port, "security": e.Security, "from": e.From, "password_changed": req.Password != nil,
	})
	a.core.Bus.Publish(core.EvSettingsUpdated, map[string]string{"section": "email"})
	writeJSON(w, http.StatusOK, e)
}

func (a *API) testEmail(w http.ResponseWriter, r *http.Request) {
	p := who(r)
	if !p.can(permManageSys) {
		forbidden(w)
		return
	}
	var req struct {
		To string `json:"to"`
	}
	if !decode(w, r, &req) {
		return
	}
	to := strings.TrimSpace(req.To)
	if to == "" {
		to = p.user.Email
	}
	if _, err := mail.ParseAddress(to); err != nil {
		badRequest(w, "enter a valid email address")
		return
	}
	if !a.loginRL.Allow("mailtest:" + p.user.ID) {
		writeErr(w, http.StatusTooManyRequests, "rate_limited", "wait a moment before sending another test")
		return
	}
	if err := a.core.SendTestEmail(r.Context(), to); err != nil {
		writeErr(w, http.StatusBadGateway, "smtp_failed", err.Error())
		return
	}
	writeJSON(w, http.StatusOK, map[string]string{"sent_to": to})
}

// ---------- country database ----------

func (a *API) geoStatus(w http.ResponseWriter, r *http.Request) {
	if !who(r).can(permRead) {
		forbidden(w)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"database": a.core.Geo.Status(), "attribution": geoip.Attribution})
}

func (a *API) geoUpdate(w http.ResponseWriter, r *http.Request) {
	if !who(r).can(permManageNet) {
		forbidden(w)
		return
	}
	if err := a.core.UpdateGeoDB(r.Context()); err != nil {
		writeErr(w, http.StatusBadGateway, "download_failed", err.Error())
		return
	}
	a.core.Audit(r.Context(), a.actor(r), "geoip.update", "settings", "geoip", a.core.Geo.Status().File, nil)
	writeJSON(w, http.StatusOK, map[string]any{"database": a.core.Geo.Status()})
}

// ---------- invitations and password resets ----------

// forgotPassword emails a reset link. It answers the same way whether or not the
// account exists, so it can't be used to find out who has an account.
func (a *API) forgotPassword(w http.ResponseWriter, r *http.Request) {
	ip := a.ClientIP(r)
	var req struct {
		Email string `json:"email"`
	}
	if !decode(w, r, &req) {
		return
	}
	email := strings.ToLower(strings.TrimSpace(req.Email))
	if !a.loginRL.Allow("forgot:"+ip) || !a.loginRL.Allow("forgot-user:"+email) {
		writeErr(w, http.StatusTooManyRequests, "rate_limited", "too many requests; try again in a few minutes")
		return
	}
	ok := map[string]string{"message": "If that address belongs to an account here, a reset link is on its way."}
	if !a.core.EmailEnabled() || email == "" {
		writeJSON(w, http.StatusOK, ok)
		return
	}
	u, err := a.core.Store.GetUserByEmail(r.Context(), email)
	if err == nil && !u.Disabled && u.Provider == "local" && a.core.Settings().Auth.PasswordLogin {
		if err := a.core.SendPasswordReset(r.Context(), u, ip); err == nil {
			a.core.Audit(r.Context(), core.Actor{ID: u.ID, Name: u.Email, IP: ip}, "auth.password_reset_requested", "user", u.ID, u.Email, nil)
		}
	}
	writeJSON(w, http.StatusOK, ok)
}

// resetPassword sets a new password with a one-time link from an invitation or reset email.
func (a *API) resetPassword(w http.ResponseWriter, r *http.Request) {
	ip := a.ClientIP(r)
	if !a.loginRL.Allow("reset:" + ip) {
		writeErr(w, http.StatusTooManyRequests, "rate_limited", "too many attempts; try again in a few minutes")
		return
	}
	var req struct {
		Token    string `json:"token"`
		Password string `json:"password"`
	}
	if !decode(w, r, &req) {
		return
	}
	// Check the password before using up the link, so a typo doesn't waste it.
	if err := auth.ValidatePassword(req.Password); err != nil {
		badRequest(w, err.Error())
		return
	}
	ctx := r.Context()
	id, ok := a.core.TakePasswordToken(ctx, req.Token)
	if !ok {
		writeErr(w, http.StatusBadRequest, "invalid_token", "this link has expired or was already used; ask for a new one")
		return
	}
	u, err := a.core.Store.GetUser(ctx, id)
	if err != nil || u.Disabled {
		writeErr(w, http.StatusBadRequest, "invalid_token", "this link has expired or was already used; ask for a new one")
		return
	}
	u.PasswordHash = secrets.HashPassword(req.Password)
	u.MustChangePassword = false
	u.LockedUntil, u.FailedLogins = 0, 0
	if err := a.core.Store.UpdateUser(ctx, u); err != nil {
		a.fail(w, r, err)
		return
	}
	_ = a.core.Store.DeleteUserSessions(ctx, u.ID) // sign out everywhere else
	a.core.Audit(ctx, core.Actor{ID: u.ID, Name: u.Email, IP: ip}, "auth.password_reset", "user", u.ID, u.Email, nil)
	writeJSON(w, http.StatusOK, map[string]string{"email": u.Email})
}

// inviteUser (re)sends the invitation email.
func (a *API) inviteUser(w http.ResponseWriter, r *http.Request) {
	u, ok := a.loadUserForAdmin(w, r)
	if !ok {
		return
	}
	if u.Provider != "local" {
		badRequest(w, "this person signs in with single sign-on; there is no password to set")
		return
	}
	if err := a.core.SendInvitation(r.Context(), u, who(r).user.Email); err != nil {
		a.fail(w, r, err)
		return
	}
	a.core.Audit(r.Context(), a.actor(r), "user.invite", "user", u.ID, u.Email, nil)
	writeJSON(w, http.StatusOK, map[string]bool{"sent": true})
}
