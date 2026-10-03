package api

import (
	"errors"
	"net/http"
	"net/netip"
	"strings"
	"time"

	"github.com/go-chi/chi/v5"

	"github.com/anand34577/gorget/internal/auth"
	"github.com/anand34577/gorget/internal/core"
	"github.com/anand34577/gorget/internal/ipam"
	"github.com/anand34577/gorget/internal/policy"
	"github.com/anand34577/gorget/internal/secrets"
	"github.com/anand34577/gorget/internal/store"
)

// ---------- first-run setup ----------

func (a *API) setupStatus(w http.ResponseWriter, r *http.Request) {
	s := a.core.Settings()
	writeJSON(w, http.StatusOK, map[string]any{
		"completed":    a.core.SetupCompleted(),
		"version":      core.Version,
		"network_name": s.Network.Name,
		"defaults": map[string]any{
			"ipv4":   s.Network.IPv4,
			"ipv6":   s.Network.IPv6,
			"domain": s.Network.Domain,
		},
		"public_url": a.core.Cfg.PublicURL,
		"tls_mode":   a.core.Cfg.TLS.Mode,
	})
}

type setupReq struct {
	NetworkName string `json:"network_name"`
	OwnerEmail  string `json:"owner_email"`
	OwnerName   string `json:"owner_name"`
	Password    string `json:"password"`
	IPv4        string `json:"ipv4"`
	Domain      string `json:"domain"`
	Policy      string `json:"policy"` // allow-all | deny
	Approval    bool   `json:"approval_required"`
	SetupToken  string `json:"setup_token"`
}

func (a *API) setup(w http.ResponseWriter, r *http.Request) {
	if a.core.SetupCompleted() {
		writeErr(w, http.StatusConflict, "setup_done", "setup has already been completed")
		return
	}
	if !a.loginRL.Allow("setup:" + a.ClientIP(r)) {
		writeErr(w, http.StatusTooManyRequests, "rate_limited", "too many attempts")
		return
	}
	var req setupReq
	if !decode(w, r, &req) {
		return
	}
	if tok := a.core.SetupToken(); tok == "" || !secrets.ConstantTimeEqual(strings.TrimSpace(req.SetupToken), tok) {
		writeErr(w, http.StatusForbidden, "bad_setup_token", "invalid setup token (see the server log or <data_dir>/setup-token)")
		return
	}
	if !strings.Contains(req.OwnerEmail, "@") {
		badRequest(w, "a valid owner email is required")
		return
	}
	if err := auth.ValidatePassword(req.Password); err != nil {
		badRequest(w, err.Error())
		return
	}
	ctx := r.Context()
	if n, err := a.core.Store.CountUsers(ctx); err != nil || n > 0 {
		writeErr(w, http.StatusConflict, "setup_done", "an account already exists")
		return
	}
	cur := a.core.Settings().Network
	ns := cur
	if req.NetworkName != "" {
		ns.Name = req.NetworkName
	}
	if req.Domain != "" {
		ns.Domain = strings.ToLower(req.Domain)
	}
	if req.IPv4 != "" {
		ns.IPv4 = req.IPv4
	}
	if err := core.ValidateNetwork(ns); err != nil {
		badRequest(w, err.Error())
		return
	}
	actor := core.Actor{ID: "setup", Name: req.OwnerEmail, IP: a.ClientIP(r)}
	if ns.IPv4 != cur.IPv4 {
		if _, err := a.core.Readdress(ctx, actor, ns.IPv4, ns.IPv6, false); err != nil {
			a.fail(w, r, err)
			return
		}
	}
	if _, err := a.core.SaveSettings(ctx, "network", func(s *core.AllSettings) error {
		s.Network.Name, s.Network.Domain = ns.Name, ns.Domain
		return nil
	}); err != nil {
		a.fail(w, r, err)
		return
	}
	if _, err := a.core.SaveSettings(ctx, "devices", func(s *core.AllSettings) error {
		s.Devices.ApprovalRequired = req.Approval
		return nil
	}); err != nil {
		a.fail(w, r, err)
		return
	}
	doc := policy.DefaultAllowAll
	if req.Policy == "deny" {
		doc = policy.DefaultDeny
	}
	if _, err := a.core.Store.SavePolicy(ctx, doc, "setup wizard", req.OwnerEmail, 0); err != nil {
		a.fail(w, r, err)
		return
	}
	u := &store.User{
		ID: secrets.RandomID(), Email: req.OwnerEmail, Name: req.OwnerName, PasswordHash: secrets.HashPassword(req.Password),
		Role: store.RoleOwner, Provider: "local", RecoveryCodes: store.StringList{}, CreatedAt: store.Now(),
	}
	if err := a.core.Store.CreateUser(ctx, u); err != nil {
		a.fail(w, r, err)
		return
	}
	if err := a.core.CompleteSetup(ctx); err != nil {
		a.fail(w, r, err)
		return
	}
	a.core.Audit(ctx, actor, "setup.complete", "network", "", ns.Name, map[string]any{"ipv4": ns.IPv4, "domain": ns.Domain, "policy": req.Policy})
	a.core.Coord.Trigger()
	res, err := a.auth.PasswordLogin(ctx, req.OwnerEmail, req.Password, a.ClientIP(r), r.UserAgent())
	if err != nil {
		a.fail(w, r, err)
		return
	}
	setSessionCookie(w, r, res.Token, a.core.Cfg.Security.SessionTTL)
	writeJSON(w, http.StatusCreated, map[string]any{"ok": true})
}

// ---------- login / logout ----------

type loginReq struct {
	Email    string `json:"email"`
	Password string `json:"password"`
}

func (a *API) login(w http.ResponseWriter, r *http.Request) {
	var req loginReq
	if !decode(w, r, &req) {
		return
	}
	ip := a.ClientIP(r)
	if !a.loginRL.Allow("login:"+ip) || !a.loginRL.Allow("login-user:"+strings.ToLower(req.Email)) {
		w.Header().Set("Retry-After", "60")
		writeErr(w, http.StatusTooManyRequests, "rate_limited", "too many login attempts; wait a minute and try again")
		return
	}
	res, err := a.auth.PasswordLogin(r.Context(), req.Email, req.Password, ip, r.UserAgent())
	switch {
	case errors.Is(err, auth.ErrBadCredentials), errors.Is(err, auth.ErrDisabled):
		writeErr(w, http.StatusUnauthorized, "bad_credentials", "invalid email or password")
		return
	case errors.Is(err, auth.ErrLocked):
		writeErr(w, http.StatusTooManyRequests, "locked", err.Error())
		return
	case errors.Is(err, auth.ErrPasswordLogin):
		writeErr(w, http.StatusForbidden, "password_login_disabled", err.Error())
		return
	case err != nil:
		a.fail(w, r, err)
		return
	}
	ttl := a.core.Cfg.Security.SessionTTL
	if res.MFAPending {
		ttl = 10 * time.Minute
	}
	setSessionCookie(w, r, res.Token, ttl)
	methods := []string{}
	if res.User.TOTPEnabled {
		methods = append(methods, "totp")
	}
	if pk, _ := a.core.Store.ListWebAuthnCredentials(r.Context(), res.User.ID); len(pk) > 0 {
		methods = append(methods, "passkey")
	}
	writeJSON(w, http.StatusOK, map[string]any{"mfa_required": res.MFAPending, "mfa_methods": methods, "csrf_token": res.Session.CSRFToken})
}

func (a *API) logout(w http.ResponseWriter, r *http.Request) {
	if c, err := r.Cookie(auth.SessionCookie); err == nil {
		a.auth.Logout(r.Context(), c.Value)
	}
	clearSessionCookie(w, r)
	writeJSON(w, http.StatusOK, map[string]bool{"ok": true})
}

func (a *API) mfaTOTP(w http.ResponseWriter, r *http.Request) {
	p := who(r)
	var req struct {
		Code string `json:"code"`
	}
	if !decode(w, r, &req) {
		return
	}
	if !a.loginRL.Allow("mfa:" + p.user.ID) {
		writeErr(w, http.StatusTooManyRequests, "rate_limited", "too many attempts")
		return
	}
	if err := a.auth.VerifyMFA(r.Context(), p.session, p.user, req.Code, a.ClientIP(r)); err != nil {
		writeErr(w, http.StatusUnauthorized, "bad_code", "invalid verification code")
		return
	}
	setSessionCookie(w, r, mustCookie(r), a.core.Cfg.Security.SessionTTL)
	writeJSON(w, http.StatusOK, map[string]bool{"ok": true})
}

func mustCookie(r *http.Request) string {
	c, _ := r.Cookie(auth.SessionCookie)
	if c == nil {
		return ""
	}
	return c.Value
}

func (a *API) mfaPasskeyBegin(w http.ResponseWriter, r *http.Request) {
	p := who(r)
	opts, err := a.auth.BeginPasskeyLogin(r.Context(), p.session, p.user)
	if err != nil {
		badRequest(w, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, opts)
}

func (a *API) mfaPasskeyFinish(w http.ResponseWriter, r *http.Request) {
	p := who(r)
	if err := a.auth.FinishPasskeyLogin(r.Context(), p.session, p.user, r, a.ClientIP(r)); err != nil {
		writeErr(w, http.StatusUnauthorized, "bad_code", err.Error())
		return
	}
	setSessionCookie(w, r, mustCookie(r), a.core.Cfg.Security.SessionTTL)
	writeJSON(w, http.StatusOK, map[string]bool{"ok": true})
}

// ---------- SSO ----------

func (a *API) ssoProviders(w http.ResponseWriter, r *http.Request) {
	provs, err := a.core.Store.ListOIDCProviders(r.Context())
	if err != nil {
		a.fail(w, r, err)
		return
	}
	out := []map[string]string{}
	for _, p := range provs {
		if p.Enabled {
			out = append(out, map[string]string{"id": p.ID, "name": p.Name})
		}
	}
	pw := a.core.Settings().Auth.PasswordLogin
	writeJSON(w, http.StatusOK, map[string]any{"providers": out, "password_login": pw, "password_reset": pw && a.core.EmailEnabled()})
}

const oidcStateCookie = "gorget_oidc_state"

func (a *API) oidcStart(w http.ResponseWriter, r *http.Request) {
	if !a.loginRL.Allow("sso:" + a.ClientIP(r)) {
		writeErr(w, http.StatusTooManyRequests, "rate_limited", "too many attempts")
		return
	}
	url, state, err := a.auth.StartOIDC(r.Context(), chi.URLParam(r, "id"), r.URL.Query().Get("return"))
	if err != nil {
		http.Redirect(w, r, "/login?error="+urlEscape(err.Error()), http.StatusFound)
		return
	}
	http.SetCookie(w, &http.Cookie{Name: oidcStateCookie, Value: state, Path: "/api/v1/auth/oidc", HttpOnly: true, Secure: isHTTPS(r), SameSite: http.SameSiteLaxMode, MaxAge: 600})
	http.Redirect(w, r, url, http.StatusFound)
}

func (a *API) oidcCallback(w http.ResponseWriter, r *http.Request) {
	q := r.URL.Query()
	if e := q.Get("error"); e != "" {
		http.Redirect(w, r, "/login?error="+urlEscape("Sign-in was cancelled or failed: "+e), http.StatusFound)
		return
	}
	cookie, _ := r.Cookie(oidcStateCookie)
	stateCookie := ""
	if cookie != nil {
		stateCookie = cookie.Value
	}
	http.SetCookie(w, &http.Cookie{Name: oidcStateCookie, Value: "", Path: "/api/v1/auth/oidc", MaxAge: -1})
	res, returnTo, err := a.auth.FinishOIDC(r.Context(), q.Get("state"), stateCookie, q.Get("code"), a.ClientIP(r), r.UserAgent())
	if err != nil {
		a.log.Warn("SSO login failed", "err", err)
		http.Redirect(w, r, "/login?error="+urlEscape(err.Error()), http.StatusFound)
		return
	}
	setSessionCookie(w, r, res.Token, a.core.Cfg.Security.SessionTTL)
	http.Redirect(w, r, returnTo, http.StatusFound)
}

func urlEscape(s string) string {
	r := strings.NewReplacer("%", "%25", " ", "%20", "&", "%26", "#", "%23", "?", "%3F", "+", "%2B", "=", "%3D")
	return r.Replace(s)
}

// ---------- me ----------

func (a *API) me(w http.ResponseWriter, r *http.Request) {
	p := who(r)
	ctx := r.Context()
	pk, _ := a.core.Store.ListWebAuthnCredentials(ctx, p.user.ID)
	s := a.core.Settings()
	resp := map[string]any{
		"user":                    p.user,
		"permissions":             rolePerms[p.user.Role],
		"mfa_enrollment_required": a.auth.MFAEnrollmentRequired(ctx, p.user),
		"passkeys":                len(pk),
		"network": map[string]any{
			"name":   s.Network.Name,
			"domain": s.Network.Domain,
		},
		"features": map[string]bool{
			"user_wg_configs": s.Auth.UserWGConfigs,
			"user_setup_keys": s.Auth.UserSetupKeys,
			"gateway":         a.core.Cfg.Gateway.Enabled,
		},
		"version":        core.Version,
		"public_url":     a.core.Cfg.PublicURL,
		"wg_tunnel_mode": s.Gateway.DefaultTunnelMode,
	}
	if p.session != nil {
		resp["csrf_token"] = p.session.CSRFToken
		resp["session_expires_at"] = p.session.ExpiresAt
	}
	writeJSON(w, http.StatusOK, resp)
}

func (a *API) updateMe(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Name string `json:"name"`
	}
	if !decode(w, r, &req) {
		return
	}
	u := who(r).user
	u.Name = strings.TrimSpace(req.Name)
	if len(u.Name) > 100 {
		badRequest(w, "name is too long")
		return
	}
	if err := a.core.Store.UpdateUser(r.Context(), u); err != nil {
		a.fail(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, u)
}

func (a *API) changePassword(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Current string `json:"current"`
		New     string `json:"new"`
	}
	if !decode(w, r, &req) {
		return
	}
	p := who(r)
	if p.session == nil {
		forbidden(w)
		return
	}
	u := p.user
	if u.Provider != "local" && u.PasswordHash == "" {
		badRequest(w, "your account signs in with single sign-on")
		return
	}
	if !secrets.VerifyPassword(u.PasswordHash, req.Current) {
		writeErr(w, http.StatusUnauthorized, "bad_credentials", "current password is incorrect")
		return
	}
	if err := auth.ValidatePassword(req.New); err != nil {
		badRequest(w, err.Error())
		return
	}
	if req.New == req.Current {
		badRequest(w, "the new password must be different")
		return
	}
	u.PasswordHash = secrets.HashPassword(req.New)
	u.MustChangePassword = false
	ctx := r.Context()
	if err := a.core.Store.UpdateUser(ctx, u); err != nil {
		a.fail(w, r, err)
		return
	}
	// Sign out every other session.
	sessions, _ := a.core.Store.ListSessions(ctx, u.ID)
	for _, s := range sessions {
		if s.ID != p.session.ID {
			_ = a.core.Store.DeleteSession(ctx, s.ID)
		}
	}
	a.core.Audit(ctx, a.actor(r), "user.change_password", "user", u.ID, u.Email, nil)
	writeJSON(w, http.StatusOK, map[string]bool{"ok": true})
}

func (a *API) reauth(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Password string `json:"password"`
	}
	if !decode(w, r, &req) {
		return
	}
	p := who(r)
	if p.session == nil {
		forbidden(w)
		return
	}
	if !a.loginRL.Allow("reauth:" + p.user.ID) {
		writeErr(w, http.StatusTooManyRequests, "rate_limited", "too many attempts")
		return
	}
	if err := a.auth.Reauth(r.Context(), p.session, p.user, req.Password); err != nil {
		writeErr(w, http.StatusUnauthorized, "bad_credentials", err.Error())
		return
	}
	writeJSON(w, http.StatusOK, map[string]bool{"ok": true})
}

// requireRecentAuth enforces re-authentication for sensitive actions (session users only).
func requireRecentAuth(w http.ResponseWriter, p *principal) bool {
	if p.session != nil && !auth.RecentlyAuthenticated(p.session) {
		writeErr(w, http.StatusForbidden, "reauth_required", "please confirm your password to continue")
		return false
	}
	return true
}

func (a *API) mySessions(w http.ResponseWriter, r *http.Request) {
	p := who(r)
	ss, err := a.core.Store.ListSessions(r.Context(), p.user.ID)
	if err != nil {
		a.fail(w, r, err)
		return
	}
	out := []map[string]any{}
	for _, s := range ss {
		if s.MFAPending {
			continue
		}
		out = append(out, map[string]any{
			"id": s.ID[:16], "created_at": s.CreatedAt, "last_seen_at": s.LastSeenAt, "expires_at": s.ExpiresAt,
			"ip": s.IP, "user_agent": s.UserAgent, "current": p.session != nil && s.ID == p.session.ID,
		})
	}
	writeJSON(w, http.StatusOK, out)
}

func (a *API) revokeMySession(w http.ResponseWriter, r *http.Request) {
	p := who(r)
	id := chi.URLParam(r, "id")
	ss, err := a.core.Store.ListSessions(r.Context(), p.user.ID)
	if err != nil {
		a.fail(w, r, err)
		return
	}
	for _, s := range ss {
		if strings.HasPrefix(s.ID, id) && len(id) >= 16 {
			_ = a.core.Store.DeleteSession(r.Context(), s.ID)
			writeJSON(w, http.StatusOK, map[string]bool{"ok": true})
			return
		}
	}
	writeErr(w, http.StatusNotFound, "not_found", "session not found")
}

func (a *API) totpBegin(w http.ResponseWriter, r *http.Request) {
	p := who(r)
	if p.session == nil {
		forbidden(w)
		return
	}
	e, err := a.auth.BeginTOTP(r.Context(), p.user)
	if err != nil {
		badRequest(w, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, e)
}

func (a *API) totpConfirm(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Code string `json:"code"`
	}
	if !decode(w, r, &req) {
		return
	}
	p := who(r)
	codes, err := a.auth.ConfirmTOTP(r.Context(), p.user, req.Code)
	if err != nil {
		badRequest(w, err.Error())
		return
	}
	a.core.Audit(r.Context(), a.actor(r), "user.mfa_enable", "user", p.user.ID, p.user.Email, map[string]string{"method": "totp"})
	writeJSON(w, http.StatusOK, map[string]any{"recovery_codes": codes})
}

func (a *API) totpDisable(w http.ResponseWriter, r *http.Request) {
	p := who(r)
	if !requireRecentAuth(w, p) {
		return
	}
	if err := a.auth.DisableTOTP(r.Context(), p.user); err != nil {
		a.fail(w, r, err)
		return
	}
	a.core.Audit(r.Context(), a.actor(r), "user.mfa_disable", "user", p.user.ID, p.user.Email, map[string]string{"method": "totp"})
	writeJSON(w, http.StatusOK, map[string]bool{"ok": true})
}

func (a *API) listPasskeys(w http.ResponseWriter, r *http.Request) {
	pk, err := a.core.Store.ListWebAuthnCredentials(r.Context(), who(r).user.ID)
	if err != nil {
		a.fail(w, r, err)
		return
	}
	if pk == nil {
		pk = []store.WebAuthnCredential{}
	}
	writeJSON(w, http.StatusOK, pk)
}

func (a *API) passkeyBegin(w http.ResponseWriter, r *http.Request) {
	p := who(r)
	if p.session == nil {
		forbidden(w)
		return
	}
	opts, err := a.auth.BeginPasskeyRegistration(r.Context(), p.user)
	if err != nil {
		badRequest(w, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, opts)
}

func (a *API) passkeyFinish(w http.ResponseWriter, r *http.Request) {
	p := who(r)
	cred, err := a.auth.FinishPasskeyRegistration(r.Context(), p.user, r.URL.Query().Get("name"), r)
	if err != nil {
		badRequest(w, err.Error())
		return
	}
	a.core.Audit(r.Context(), a.actor(r), "user.mfa_enable", "user", p.user.ID, p.user.Email, map[string]string{"method": "passkey", "name": cred.Name})
	writeJSON(w, http.StatusCreated, cred)
}

func (a *API) deletePasskey(w http.ResponseWriter, r *http.Request) {
	p := who(r)
	if !requireRecentAuth(w, p) {
		return
	}
	if err := a.core.Store.DeleteWebAuthnCredential(r.Context(), p.user.ID, chi.URLParam(r, "id")); err != nil {
		a.fail(w, r, err)
		return
	}
	a.core.Audit(r.Context(), a.actor(r), "user.mfa_disable", "user", p.user.ID, p.user.Email, map[string]string{"method": "passkey"})
	writeJSON(w, http.StatusOK, map[string]bool{"ok": true})
}

// ---------- API tokens ----------

func (a *API) listTokens(w http.ResponseWriter, r *http.Request) {
	ts, err := a.core.Store.ListAPITokens(r.Context(), who(r).user.ID)
	if err != nil {
		a.fail(w, r, err)
		return
	}
	if ts == nil {
		ts = []store.APIToken{}
	}
	writeJSON(w, http.StatusOK, ts)
}

func (a *API) listAllTokens(w http.ResponseWriter, r *http.Request) {
	if !who(r).can(permManageSys) {
		forbidden(w)
		return
	}
	ts, err := a.core.Store.ListAPITokens(r.Context(), "")
	if err != nil {
		a.fail(w, r, err)
		return
	}
	if ts == nil {
		ts = []store.APIToken{}
	}
	writeJSON(w, http.StatusOK, ts)
}

func (a *API) createToken(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Name          string   `json:"name"`
		Scopes        []string `json:"scopes"`
		ExpiresInDays int      `json:"expires_in_days"`
	}
	if !decode(w, r, &req) {
		return
	}
	p := who(r)
	if p.session == nil {
		writeErr(w, http.StatusForbidden, "forbidden", "API tokens cannot create other tokens")
		return
	}
	if strings.TrimSpace(req.Name) == "" {
		badRequest(w, "name is required")
		return
	}
	scopes := []string{"read"}
	for _, s := range req.Scopes {
		if s == "write" {
			scopes = append(scopes, "write")
		} else if s != "read" {
			badRequest(w, "scopes must be read and/or write")
			return
		}
	}
	var exp int64
	if req.ExpiresInDays > 0 {
		exp = time.Now().AddDate(0, 0, req.ExpiresInDays).Unix()
	}
	t, token, err := a.auth.CreateAPIToken(r.Context(), p.user, strings.TrimSpace(req.Name), scopes, exp)
	if err != nil {
		a.fail(w, r, err)
		return
	}
	a.core.Audit(r.Context(), a.actor(r), "token.create", "api_token", t.ID, t.Name, map[string]any{"scopes": scopes})
	writeJSON(w, http.StatusCreated, map[string]any{"token": token, "api_token": t})
}

func (a *API) deleteToken(w http.ResponseWriter, r *http.Request) {
	p := who(r)
	owner := p.user.ID
	if p.can(permManageSys) {
		owner = ""
	}
	if err := a.core.Store.DeleteAPIToken(r.Context(), chi.URLParam(r, "id"), owner); err != nil {
		a.fail(w, r, err)
		return
	}
	a.core.Audit(r.Context(), a.actor(r), "token.delete", "api_token", chi.URLParam(r, "id"), "", nil)
	writeJSON(w, http.StatusOK, map[string]bool{"ok": true})
}

// ensure imports used
var _ = netip.Addr{}
var _ = ipam.DefaultIPv4
