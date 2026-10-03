// Package api implements the REST/JSON admin and self-service API used by the
// web console, the CLI and third-party automation.
package api

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"log/slog"
	"net/http"
	"slices"
	"strconv"
	"strings"
	"time"

	"github.com/go-chi/chi/v5"

	"github.com/anand34577/gorget/internal/auth"
	"github.com/anand34577/gorget/internal/core"
	"github.com/anand34577/gorget/internal/gateway"
	"github.com/anand34577/gorget/internal/store"
)

// SystemInfo supplies runtime status from components outside the API (TLS, relay...).
type SystemInfo interface {
	TLSStatus() any
	RelayStats() any
	STUNStats() any
}

type API struct {
	core    *core.Core
	auth    *auth.Manager
	gw      *gateway.Gateway
	sys     SystemInfo
	log     *slog.Logger
	loginRL *auth.Limiter
	apiRL   *auth.Limiter
	// ClientIP resolves the real client IP for a request.
	ClientIP func(*http.Request) string
}

func New(c *core.Core, a *auth.Manager, gw *gateway.Gateway, sys SystemInfo, log *slog.Logger, clientIP func(*http.Request) string) *API {
	return &API{
		core: c, auth: a, gw: gw, sys: sys, log: log, ClientIP: clientIP,
		loginRL: auth.NewLimiter(10, 10),
		apiRL:   auth.NewLimiter(600, 200),
	}
}

// ---------- principal ----------

type principal struct {
	user    *store.User
	session *store.Session  // nil for API tokens
	token   *store.APIToken // nil for sessions
}

func (p *principal) actor(ip string) core.Actor {
	a := core.Actor{ID: p.user.ID, Name: p.user.Email, IP: ip, Role: p.user.Role, Token: p.token != nil}
	if p.token != nil {
		a.Name += " (token " + p.token.Name + ")"
	}
	return a
}

type ctxKey int

const principalKey ctxKey = 1

func who(r *http.Request) *principal {
	p, _ := r.Context().Value(principalKey).(*principal)
	return p
}

// Permissions.
const (
	permRead        = "read"         // read everything (admins, auditors)
	permManageNet   = "manage_net"   // devices, routes, policy, dns, keys, wg configs, groups
	permManageUsers = "manage_users" // users, groups
	permManageSys   = "manage_sys"   // settings, sso, webhooks, tokens of others
	permOwner       = "owner"        // owner-only operations
)

var rolePerms = map[string][]string{
	store.RoleOwner:        {permRead, permManageNet, permManageUsers, permManageSys, permOwner},
	store.RoleAdmin:        {permRead, permManageNet, permManageUsers, permManageSys},
	store.RoleNetworkAdmin: {permRead, permManageNet},
	store.RoleAuditor:      {permRead},
	store.RoleUser:         {},
}

func (p *principal) can(perm string) bool {
	if p == nil {
		return false
	}
	if !slices.Contains(rolePerms[p.user.Role], perm) {
		return false
	}
	// Read-only API tokens may only read.
	if p.token != nil && perm != permRead && !slices.Contains([]string(p.token.Scopes), "write") {
		return false
	}
	return true
}

// canWrite reports whether the principal may perform non-GET requests at all.
func (p *principal) canWrite() bool {
	return p.token == nil || slices.Contains([]string(p.token.Scopes), "write")
}

// ---------- router ----------

func (a *API) Routes() http.Handler {
	r := chi.NewRouter()
	r.Use(a.recoverer)
	r.Use(a.rateLimit)

	r.Get("/openapi.json", a.openAPI)
	r.Get("/setup/status", a.setupStatus)
	r.Post("/setup", a.setup)

	r.Route("/auth", func(r chi.Router) {
		r.Post("/login", a.login)
		r.Post("/logout", a.logout)
		r.Get("/providers", a.ssoProviders)
		r.Post("/forgot-password", a.forgotPassword)
		r.Post("/reset-password", a.resetPassword)
		r.Get("/oidc/{id}/start", a.oidcStart)
		r.Get("/oidc/callback", a.oidcCallback)
		r.Group(func(r chi.Router) {
			r.Use(a.requireSession(true))
			r.Post("/mfa/totp", a.mfaTOTP)
			r.Post("/mfa/passkey/begin", a.mfaPasskeyBegin)
			r.Post("/mfa/passkey/finish", a.mfaPasskeyFinish)
		})
	})

	r.Group(func(r chi.Router) {
		r.Use(a.requireSession(false))

		r.Get("/me", a.me)
		r.Patch("/me", a.updateMe)
		r.Post("/me/password", a.changePassword)
		r.Post("/me/reauth", a.reauth)
		r.Get("/me/sessions", a.mySessions)
		r.Delete("/me/sessions/{id}", a.revokeMySession)
		r.Post("/me/totp/begin", a.totpBegin)
		r.Post("/me/totp/confirm", a.totpConfirm)
		r.Delete("/me/totp", a.totpDisable)
		r.Get("/me/passkeys", a.listPasskeys)
		r.Post("/me/passkeys/begin", a.passkeyBegin)
		r.Post("/me/passkeys/finish", a.passkeyFinish)
		r.Delete("/me/passkeys/{id}", a.deletePasskey)
		r.Get("/me/tokens", a.listTokens)
		r.Post("/me/tokens", a.createToken)
		r.Delete("/me/tokens/{id}", a.deleteToken)

		r.Get("/overview", a.overview)
		r.Get("/events", a.events)
		r.Get("/network-map", a.networkMap)
		r.Get("/stats", a.stats)
		r.Get("/geoip", a.geoStatus)
		r.Post("/geoip/update", a.geoUpdate)

		r.Get("/devices", a.listDevices)
		r.Get("/devices/{id}", a.getDevice)
		r.Patch("/devices/{id}", a.updateDevice)
		r.Delete("/devices/{id}", a.deleteDevice)
		r.Post("/devices/{id}/approve", a.approveDevice)
		r.Post("/devices/{id}/expire-key", a.expireDeviceKey)
		r.Get("/devices/{id}/access", a.deviceAccess)
		r.Get("/devices/{id}/sessions", a.deviceSessions)
		r.Get("/devices/{id}/wireguard-config", a.wgConfig)
		r.Post("/devices/{id}/rotate-psk", a.rotatePSK)
		r.Post("/wireguard-configs", a.createWGConfig)

		r.Get("/device-logins/{code}", a.getDeviceLogin)
		r.Post("/device-logins/{code}/approve", a.approveDeviceLogin)
		r.Post("/device-logins/{code}/deny", a.denyDeviceLogin)

		r.Get("/users", a.listUsers)
		r.Post("/users", a.createUser)
		r.Patch("/users/{id}", a.updateUser)
		r.Delete("/users/{id}", a.deleteUser)
		r.Post("/users/{id}/reset-mfa", a.resetUserMFA)
		r.Post("/users/{id}/reset-password", a.resetUserPassword)
		r.Post("/users/{id}/invite", a.inviteUser)
		r.Post("/users/{id}/revoke-sessions", a.revokeUserSessions)

		r.Get("/groups", a.listGroups)
		r.Post("/groups", a.createGroup)
		r.Patch("/groups/{id}", a.updateGroup)
		r.Delete("/groups/{id}", a.deleteGroup)

		r.Get("/policy", a.getPolicy)
		r.Put("/policy", a.putPolicy)
		r.Post("/policy/validate", a.validatePolicy)
		r.Post("/policy/check", a.checkPolicy)
		r.Get("/policy/versions", a.policyVersions)
		r.Get("/policy/versions/{v}", a.policyVersion)
		r.Post("/policy/versions/{v}/restore", a.restorePolicy)

		r.Get("/scim", a.scimStatus)
		r.Post("/scim/token", a.scimRotate)
		r.Delete("/scim", a.scimDisable)

		r.Get("/access-requests", a.listAccessRequests)
		r.Post("/access-requests", a.createAccessRequest)
		r.Post("/access-requests/{id}/approve", a.decideAccess("approve"))
		r.Post("/access-requests/{id}/deny", a.decideAccess("deny"))
		r.Post("/access-requests/{id}/revoke", a.decideAccess("revoke"))
		r.Post("/access-grants", a.grantAccess)

		r.Get("/routes", a.listRoutes)
		r.Post("/routes", a.createRoute)
		r.Patch("/routes/{id}", a.updateRoute)
		r.Delete("/routes/{id}", a.deleteRoute)

		r.Get("/setup-keys", a.listSetupKeys)
		r.Post("/setup-keys", a.createSetupKey)
		r.Post("/setup-keys/{id}/revoke", a.revokeSetupKey)
		r.Delete("/setup-keys/{id}", a.deleteSetupKey)

		r.Get("/settings", a.getSettings)
		r.Get("/settings/email", a.emailStatus)
		r.Put("/settings/email", a.putEmail)
		r.Post("/settings/email/test", a.testEmail)
		r.Get("/settings/notifications", a.notificationsStatus)
		r.Put("/settings/notifications", a.putNotifications)
		r.Post("/settings/notifications/test", a.testNotification)
		r.Put("/settings/{section}", a.putSettings)
		r.Post("/settings/network/readdress", a.readdress)

		r.Get("/sso-providers", a.listOIDC)
		r.Post("/sso-providers", a.createOIDC)
		r.Patch("/sso-providers/{id}", a.updateOIDC)
		r.Delete("/sso-providers/{id}", a.deleteOIDC)
		r.Post("/sso-providers/test", a.testOIDC)

		r.Get("/webhooks", a.listWebhooks)
		r.Post("/webhooks", a.createWebhook)
		r.Patch("/webhooks/{id}", a.updateWebhook)
		r.Delete("/webhooks/{id}", a.deleteWebhook)
		r.Post("/webhooks/{id}/test", a.testWebhook)

		r.Get("/audit", a.listAudit)
		r.Get("/audit/verify", a.verifyAudit)
		r.Get("/system/status", a.systemStatus)
		r.Get("/api-tokens", a.listAllTokens)
	})
	return r
}

// ---------- middleware ----------

func (a *API) recoverer(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		defer func() {
			if v := recover(); v != nil {
				if v == http.ErrAbortHandler {
					panic(v)
				}
				a.log.Error("panic in API handler", "path", r.URL.Path, "panic", v)
				writeErr(w, http.StatusInternalServerError, "internal", "internal server error")
			}
		}()
		next.ServeHTTP(w, r)
	})
}

func (a *API) rateLimit(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if !a.apiRL.Allow(a.ClientIP(r)) {
			w.Header().Set("Retry-After", "10")
			writeErr(w, http.StatusTooManyRequests, "rate_limited", "too many requests")
			return
		}
		next.ServeHTTP(w, r)
	})
}

// requireSession authenticates via cookie session or bearer API token.
// allowMFAPending permits sessions that still need their second factor.
func (a *API) requireSession(allowMFAPending bool) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			ctx := r.Context()
			var p *principal
			if bearer := strings.TrimPrefix(r.Header.Get("Authorization"), "Bearer "); bearer != "" && bearer != r.Header.Get("Authorization") {
				t, u, err := a.auth.APIToken(ctx, bearer)
				if err != nil {
					writeErr(w, http.StatusUnauthorized, "unauthenticated", "invalid or expired API token")
					return
				}
				p = &principal{user: u, token: t}
				if r.Method != http.MethodGet && !p.canWrite() {
					writeErr(w, http.StatusForbidden, "forbidden", "this API token is read-only")
					return
				}
			} else {
				c, err := r.Cookie(auth.SessionCookie)
				if err != nil {
					writeErr(w, http.StatusUnauthorized, "unauthenticated", "please sign in")
					return
				}
				se, u, err := a.auth.Session(ctx, c.Value)
				if err != nil {
					clearSessionCookie(w, r)
					writeErr(w, http.StatusUnauthorized, "unauthenticated", "your session has expired; please sign in again")
					return
				}
				if r.Method != http.MethodGet && r.Method != http.MethodHead {
					if hdr := r.Header.Get("X-CSRF-Token"); hdr == "" || hdr != se.CSRFToken {
						writeErr(w, http.StatusForbidden, "csrf", "missing or invalid CSRF token")
						return
					}
				}
				if se.MFAPending && !allowMFAPending {
					writeErr(w, http.StatusUnauthorized, "mfa_required", "second factor required")
					return
				}
				p = &principal{user: u, session: se}
				// Enforced MFA enrolment / password change: only self-service endpoints allowed.
				if !allowMFAPending && !strings.HasPrefix(r.URL.Path, "/api/v1/me") &&
					(u.MustChangePassword || a.auth.MFAEnrollmentRequired(ctx, u)) {
					code := "mfa_enrollment_required"
					if u.MustChangePassword {
						code = "password_change_required"
					}
					writeErr(w, http.StatusForbidden, code, "complete your account setup first")
					return
				}
			}
			next.ServeHTTP(w, r.WithContext(context.WithValue(ctx, principalKey, p)))
		})
	}
}

func setSessionCookie(w http.ResponseWriter, r *http.Request, token string, ttl time.Duration) {
	http.SetCookie(w, &http.Cookie{
		Name:     auth.SessionCookie,
		Value:    token,
		Path:     "/",
		HttpOnly: true,
		Secure:   isHTTPS(r),
		SameSite: http.SameSiteLaxMode,
		MaxAge:   int(ttl.Seconds()),
	})
}

func clearSessionCookie(w http.ResponseWriter, r *http.Request) {
	http.SetCookie(w, &http.Cookie{Name: auth.SessionCookie, Value: "", Path: "/", HttpOnly: true, Secure: isHTTPS(r), SameSite: http.SameSiteLaxMode, MaxAge: -1})
}

func isHTTPS(r *http.Request) bool {
	return r.TLS != nil || r.Header.Get("X-Forwarded-Proto") == "https"
}

// ---------- helpers ----------

type apiError struct {
	Code    string `json:"code"`
	Message string `json:"message"`
	Details any    `json:"details,omitempty"`
}

func writeJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.Header().Set("Cache-Control", "no-store")
	w.WriteHeader(status)
	if v != nil {
		_ = json.NewEncoder(w).Encode(v)
	}
}

func writeErr(w http.ResponseWriter, status int, code, msg string) {
	writeJSON(w, status, map[string]apiError{"error": {Code: code, Message: msg}})
}

func writeErrDetails(w http.ResponseWriter, status int, code, msg string, details any) {
	writeJSON(w, status, map[string]apiError{"error": {Code: code, Message: msg, Details: details}})
}

// fail maps domain errors to HTTP responses.
func (a *API) fail(w http.ResponseWriter, r *http.Request, err error) {
	var ie *core.InvalidError
	switch {
	case errors.As(err, &ie):
		writeErr(w, http.StatusBadRequest, "invalid", ie.Msg)
	case errors.Is(err, store.ErrNotFound):
		writeErr(w, http.StatusNotFound, "not_found", "not found")
	case errors.Is(err, store.ErrConflict):
		writeErr(w, http.StatusConflict, "conflict", "already exists or was modified concurrently")
	case errors.Is(err, core.ErrForbidden):
		writeErr(w, http.StatusForbidden, "forbidden", "you do not have permission to do this")
	default:
		a.log.Error("API error", "method", r.Method, "path", r.URL.Path, "err", err)
		writeErr(w, http.StatusInternalServerError, "internal", "internal server error")
	}
}

func forbidden(w http.ResponseWriter) {
	writeErr(w, http.StatusForbidden, "forbidden", "you do not have permission to do this")
}

func badRequest(w http.ResponseWriter, msg string) {
	writeErr(w, http.StatusBadRequest, "invalid", msg)
}

// decode reads a JSON body (max 1 MiB) and rejects unknown fields.
func decode(w http.ResponseWriter, r *http.Request, v any) bool {
	dec := json.NewDecoder(io.LimitReader(r.Body, 1<<20))
	dec.DisallowUnknownFields()
	if err := dec.Decode(v); err != nil {
		badRequest(w, "invalid JSON body: "+err.Error())
		return false
	}
	return true
}

func (a *API) actor(r *http.Request) core.Actor { return who(r).actor(a.ClientIP(r)) }

func queryInt(r *http.Request, key string, def int) int {
	if v, err := strconv.Atoi(r.URL.Query().Get(key)); err == nil {
		return v
	}
	return def
}

func queryInt64(r *http.Request, key string) int64 {
	v, _ := strconv.ParseInt(r.URL.Query().Get(key), 10, 64)
	return v
}
