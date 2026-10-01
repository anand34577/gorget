// Package auth implements user authentication: password login with lockout,
// sessions, TOTP and WebAuthn second factors, OIDC single sign-on and API tokens.
package auth

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"time"

	"github.com/anand34577/gorget/internal/core"
	"github.com/anand34577/gorget/internal/secrets"
	"github.com/anand34577/gorget/internal/store"
)

var (
	ErrBadCredentials = errors.New("invalid email or password")
	ErrLocked         = errors.New("account temporarily locked after too many failed attempts")
	ErrDisabled       = errors.New("account disabled")
	ErrPasswordLogin  = errors.New("password login is disabled; use single sign-on")
	ErrBadCode        = errors.New("invalid verification code")
	ErrNoSession      = errors.New("not authenticated")
)

const (
	SessionCookie  = "gorget_session"
	sessionPrefix  = "gs_"
	apiTokenPrefix = "gat_"
)

type Manager struct {
	core *core.Core
	st   *store.Store
}

func New(c *core.Core) *Manager {
	return &Manager{core: c, st: c.Store}
}

// putPending stores short-lived state (sign-in ceremonies) in the database, so that the
// request finishing a ceremony may land on a different server instance than the one that began it.
func (m *Manager) putPending(ctx context.Context, key string, v any, ttl time.Duration) error {
	b, err := json.Marshal(v)
	if err != nil {
		return err
	}
	return m.st.PutPending(ctx, "auth:"+key, string(b), int64(ttl.Seconds()))
}

// takePending loads and removes the state exactly once.
func (m *Manager) takePending(ctx context.Context, key string, dst any) bool {
	v, ok, err := m.st.TakePending(ctx, "auth:"+key)
	if err != nil || !ok {
		return false
	}
	return json.Unmarshal([]byte(v), dst) == nil
}

// LoginResult is returned by a successful first-factor login.
type LoginResult struct {
	Token      string
	Session    *store.Session
	User       *store.User
	MFAPending bool
}

// PasswordLogin verifies credentials and creates a session.
func (m *Manager) PasswordLogin(ctx context.Context, email, password, ip, ua string) (*LoginResult, error) {
	if !m.core.Settings().Auth.PasswordLogin {
		// The owner can always log in with a password as a recovery path.
		u, err := m.st.GetUserByEmail(ctx, email)
		if err != nil || u.Role != store.RoleOwner {
			secrets.BurnPasswordCheck(password)
			return nil, ErrPasswordLogin
		}
	}
	u, err := m.st.GetUserByEmail(ctx, email)
	if errors.Is(err, store.ErrNotFound) {
		secrets.BurnPasswordCheck(password)
		return nil, ErrBadCredentials
	}
	if err != nil {
		return nil, err
	}
	now := time.Now()
	if u.LockedUntil > now.Unix() {
		secrets.BurnPasswordCheck(password)
		return nil, ErrLocked
	}
	if u.PasswordHash == "" || !secrets.VerifyPassword(u.PasswordHash, password) {
		as := m.core.Settings().Auth
		u.FailedLogins++
		locked := false
		if as.LockoutThreshold > 0 && u.FailedLogins >= as.LockoutThreshold {
			u.LockedUntil = now.Add(time.Duration(max(as.LockoutMinutes, 1)) * time.Minute).Unix()
			u.FailedLogins = 0
			locked = true
		}
		_ = m.st.UpdateUser(ctx, u)
		m.core.Bus.Publish(core.EvLoginFailed, map[string]any{"id": u.ID, "email": u.Email, "ip": ip, "locked": locked, "locked_until": u.LockedUntil})
		m.core.Audit(ctx, core.Actor{ID: u.ID, Name: u.Email, IP: ip}, "auth.login_failed", "user", u.ID, u.Email, nil)
		return nil, ErrBadCredentials
	}
	if u.Disabled {
		return nil, ErrDisabled
	}
	return m.startSession(ctx, u, ip, ua, "password")
}

// startSession creates a session after a successful first factor.
func (m *Manager) startSession(ctx context.Context, u *store.User, ip, ua, method string) (*LoginResult, error) {
	hasMFA, err := m.HasMFA(ctx, u)
	if err != nil {
		return nil, err
	}
	// SSO users authenticate their second factor at the identity provider.
	mfaPending := hasMFA && method == "password"
	token := secrets.RandomToken(sessionPrefix, 32)
	now := store.Now()
	ttl := m.core.Cfg.Security.SessionTTL
	se := &store.Session{
		ID:         secrets.HashToken(token),
		UserID:     u.ID,
		CSRFToken:  secrets.RandomToken("", 24),
		MFAPending: mfaPending,
		CreatedAt:  now,
		ExpiresAt:  now + int64(ttl.Seconds()),
		LastSeenAt: now,
		ReauthAt:   now,
		IP:         ip,
		UserAgent:  truncate(ua, 256),
	}
	if mfaPending {
		// Short window to complete the second factor.
		se.ExpiresAt = now + 600
	}
	if err := m.st.CreateSession(ctx, se); err != nil {
		return nil, err
	}
	if !mfaPending {
		m.completeLogin(ctx, u, ip, method)
	}
	return &LoginResult{Token: token, Session: se, User: u, MFAPending: mfaPending}, nil
}

func (m *Manager) completeLogin(ctx context.Context, u *store.User, ip, method string) {
	u.FailedLogins = 0
	u.LastLoginAt = store.Now()
	_ = m.st.UpdateUser(ctx, u)
	m.core.Audit(ctx, core.Actor{ID: u.ID, Name: u.Email, IP: ip}, "auth.login", "user", u.ID, u.Email, map[string]string{"method": method})
}

// HasMFA reports whether the user has any second factor enrolled.
func (m *Manager) HasMFA(ctx context.Context, u *store.User) (bool, error) {
	if u.TOTPEnabled {
		return true, nil
	}
	creds, err := m.st.ListWebAuthnCredentials(ctx, u.ID)
	return len(creds) > 0, err
}

// MFAEnrollmentRequired reports whether policy requires u to enrol a second factor.
func (m *Manager) MFAEnrollmentRequired(ctx context.Context, u *store.User) bool {
	if u.Provider != "local" {
		return false
	}
	as := m.core.Settings().Auth
	if !as.MFARequired && !(as.MFARequiredAdmins && u.Role != store.RoleUser && u.Role != store.RoleAuditor) {
		return false
	}
	has, err := m.HasMFA(ctx, u)
	return err == nil && !has
}

// Session validates a session token. It extends the idle window.
func (m *Manager) Session(ctx context.Context, token string) (*store.Session, *store.User, error) {
	if !strings.HasPrefix(token, sessionPrefix) {
		return nil, nil, ErrNoSession
	}
	se, err := m.st.GetSession(ctx, secrets.HashToken(token))
	if err != nil {
		return nil, nil, ErrNoSession
	}
	now := store.Now()
	if now > se.ExpiresAt {
		_ = m.st.DeleteSession(ctx, se.ID)
		return nil, nil, ErrNoSession
	}
	u, err := m.st.GetUser(ctx, se.UserID)
	if err != nil || u.Disabled {
		_ = m.st.DeleteSession(ctx, se.ID)
		return nil, nil, ErrNoSession
	}
	if now-se.LastSeenAt > 60 {
		se.LastSeenAt = now
		_ = m.st.UpdateSession(ctx, se)
	}
	return se, u, nil
}

func (m *Manager) Logout(ctx context.Context, token string) {
	_ = m.st.DeleteSession(ctx, secrets.HashToken(token))
}

// Reauth confirms the user's password for sensitive actions.
func (m *Manager) Reauth(ctx context.Context, se *store.Session, u *store.User, password string) error {
	if u.Provider != "local" || u.PasswordHash == "" {
		// SSO users: require a fresh login within the last 15 minutes instead.
		if store.Now()-se.CreatedAt > 900 {
			return errors.New("please sign in again to confirm this action")
		}
		return nil
	}
	if !secrets.VerifyPassword(u.PasswordHash, password) {
		return ErrBadCredentials
	}
	se.ReauthAt = store.Now()
	return m.st.UpdateSession(ctx, se)
}

// RecentlyAuthenticated reports whether a sensitive action may proceed without re-auth.
func RecentlyAuthenticated(se *store.Session) bool { return store.Now()-se.ReauthAt < 600 }

// ---------- API tokens ----------

// CreateAPIToken returns the plaintext token once.
func (m *Manager) CreateAPIToken(ctx context.Context, u *store.User, name string, scopes []string, expiresAt int64) (*store.APIToken, string, error) {
	token := secrets.RandomToken(apiTokenPrefix, 32)
	t := &store.APIToken{
		ID:          secrets.RandomID(),
		Name:        name,
		UserID:      u.ID,
		TokenHash:   secrets.HashToken(token),
		TokenPrefix: token[:len(apiTokenPrefix)+6],
		Scopes:      scopes,
		ExpiresAt:   expiresAt,
		CreatedAt:   store.Now(),
	}
	return t, token, m.st.CreateAPIToken(ctx, t)
}

// APIToken validates a bearer API token.
func (m *Manager) APIToken(ctx context.Context, token string) (*store.APIToken, *store.User, error) {
	if !strings.HasPrefix(token, apiTokenPrefix) {
		return nil, nil, ErrNoSession
	}
	t, err := m.st.GetAPITokenByHash(ctx, secrets.HashToken(token))
	if err != nil {
		return nil, nil, ErrNoSession
	}
	if t.ExpiresAt > 0 && store.Now() > t.ExpiresAt {
		return nil, nil, ErrNoSession
	}
	u, err := m.st.GetUser(ctx, t.UserID)
	if err != nil || u.Disabled {
		return nil, nil, ErrNoSession
	}
	if store.Now()-t.LastUsedAt > 60 {
		_ = m.st.TouchAPIToken(ctx, t.ID)
	}
	return t, u, nil
}

func truncate(s string, n int) string {
	if len(s) > n {
		return s[:n]
	}
	return s
}

// ValidatePassword enforces a minimal password policy.
func ValidatePassword(p string) error {
	if len(p) < 10 {
		return errors.New("password must be at least 10 characters")
	}
	if len(p) > 256 {
		return errors.New("password is too long")
	}
	return nil
}
