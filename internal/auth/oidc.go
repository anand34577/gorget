package auth

import (
	"context"
	"crypto/sha256"
	"encoding/base64"
	"errors"
	"fmt"
	"slices"
	"strings"
	"time"

	"github.com/coreos/go-oidc/v3/oidc"
	"golang.org/x/oauth2"

	"github.com/anand34577/gorget/internal/core"
	"github.com/anand34577/gorget/internal/secrets"
	"github.com/anand34577/gorget/internal/store"
)

type oidcState struct {
	ProviderID string
	Nonce      string
	Verifier   string
	ReturnTo   string
}

func (m *Manager) callbackURL() string { return m.core.Cfg.PublicURL + "/api/v1/auth/oidc/callback" }

func (m *Manager) oauthConfig(ctx context.Context, p *store.OIDCProvider) (*oauth2.Config, *oidc.Provider, error) {
	cctx, cancel := context.WithTimeout(ctx, 15*time.Second)
	defer cancel()
	prov, err := oidc.NewProvider(cctx, p.Issuer)
	if err != nil {
		return nil, nil, fmt.Errorf("discover %s: %w", p.Issuer, err)
	}
	secret, err := m.core.Box.Open(p.ClientSecret)
	if err != nil {
		return nil, nil, err
	}
	scopes := []string{oidc.ScopeOpenID, "email", "profile"}
	for _, s := range p.Scopes {
		if !slices.Contains(scopes, s) {
			scopes = append(scopes, s)
		}
	}
	return &oauth2.Config{
		ClientID:     p.ClientID,
		ClientSecret: secret,
		Endpoint:     prov.Endpoint(),
		RedirectURL:  m.callbackURL(),
		Scopes:       scopes,
	}, prov, nil
}

// TestOIDCProvider checks discovery for a provider configuration.
func (m *Manager) TestOIDCProvider(ctx context.Context, issuer string) error {
	cctx, cancel := context.WithTimeout(ctx, 15*time.Second)
	defer cancel()
	_, err := oidc.NewProvider(cctx, issuer)
	return err
}

// StartOIDC returns the IdP authorization URL and the opaque state key.
func (m *Manager) StartOIDC(ctx context.Context, providerID, returnTo string) (string, string, error) {
	p, err := m.st.GetOIDCProvider(ctx, providerID)
	if err != nil || !p.Enabled {
		return "", "", errors.New("unknown sign-in provider")
	}
	conf, _, err := m.oauthConfig(ctx, p)
	if err != nil {
		return "", "", err
	}
	state := secrets.RandomToken("", 24)
	st := oidcState{ProviderID: p.ID, Nonce: secrets.RandomToken("", 16), Verifier: oauth2.GenerateVerifier(), ReturnTo: safeReturn(returnTo)}
	if err := m.putPending(ctx, "oidc:"+state, st, 10*time.Minute); err != nil {
		return "", "", err
	}
	authURL := conf.AuthCodeURL(state, oidc.Nonce(st.Nonce), oauth2.S256ChallengeOption(st.Verifier))
	return authURL, state, nil
}

func safeReturn(s string) string {
	if !strings.HasPrefix(s, "/") || strings.HasPrefix(s, "//") || strings.Contains(s, "\\") {
		return "/"
	}
	return s
}

type oidcClaims struct {
	Subject       string `json:"sub"`
	Email         string `json:"email"`
	EmailVerified *bool  `json:"email_verified"`
	Name          string `json:"name"`
	PreferredUser string `json:"preferred_username"`
}

// FinishOIDC exchanges the code, maps or provisions the user and starts a session.
// stateCookie must equal state (double-submit binding to the browser).
func (m *Manager) FinishOIDC(ctx context.Context, state, stateCookie, code, ip, ua string) (*LoginResult, string, error) {
	if state == "" || !secrets.ConstantTimeEqual(state, stateCookie) {
		return nil, "", errors.New("sign-in session mismatch; please try again")
	}
	var st oidcState
	if !m.takePending(ctx, "oidc:"+state, &st) {
		return nil, "", errors.New("sign-in expired; please try again")
	}
	p, err := m.st.GetOIDCProvider(ctx, st.ProviderID)
	if err != nil {
		return nil, "", err
	}
	conf, prov, err := m.oauthConfig(ctx, p)
	if err != nil {
		return nil, "", err
	}
	tok, err := conf.Exchange(ctx, code, oauth2.VerifierOption(st.Verifier))
	if err != nil {
		return nil, "", fmt.Errorf("token exchange failed: %w", err)
	}
	rawID, ok := tok.Extra("id_token").(string)
	if !ok {
		return nil, "", errors.New("identity provider returned no id_token")
	}
	idTok, err := prov.Verifier(&oidc.Config{ClientID: p.ClientID}).Verify(ctx, rawID)
	if err != nil {
		return nil, "", fmt.Errorf("invalid id_token: %w", err)
	}
	if subtleNonce(idTok.Nonce) != subtleNonce(st.Nonce) {
		return nil, "", errors.New("invalid nonce")
	}
	var claims oidcClaims
	if err := idTok.Claims(&claims); err != nil {
		return nil, "", err
	}
	var all map[string]any
	_ = idTok.Claims(&all)
	// Fill missing claims from the userinfo endpoint.
	if claims.Email == "" {
		if ui, err := prov.UserInfo(ctx, oauth2.StaticTokenSource(tok)); err == nil {
			claims.Email = ui.Email
			v := ui.EmailVerified
			claims.EmailVerified = &v
			_ = ui.Claims(&all)
		}
	}
	email := strings.ToLower(strings.TrimSpace(claims.Email))
	if email == "" {
		return nil, "", errors.New("identity provider did not return an email address")
	}
	if claims.EmailVerified != nil && !*claims.EmailVerified {
		return nil, "", errors.New("your email address is not verified at the identity provider")
	}
	if len(p.AllowedDomains) > 0 {
		domain := email[strings.LastIndex(email, "@")+1:]
		if !slices.Contains([]string(p.AllowedDomains), domain) {
			return nil, "", errors.New("your email domain is not allowed to sign in")
		}
	}

	provKey := "oidc:" + p.ID
	u, err := m.st.GetUserBySubject(ctx, provKey, idTok.Subject)
	if errors.Is(err, store.ErrNotFound) {
		// Link to an existing account with the same (verified) email, or provision.
		u, err = m.st.GetUserByEmail(ctx, email)
		switch {
		case err == nil:
			if u.Provider != "local" && u.Provider != provKey {
				return nil, "", errors.New("this email is linked to another sign-in provider")
			}
			u.Provider, u.ProviderSubject = provKey, idTok.Subject
			if err := m.st.UpdateUser(ctx, u); err != nil {
				return nil, "", err
			}
		case errors.Is(err, store.ErrNotFound):
			if !p.AutoCreateUsers {
				return nil, "", errors.New("no account exists for this email; ask an administrator to invite you")
			}
			role := p.DefaultRole
			if role == "" || role == store.RoleOwner || !store.ValidRole(role) {
				role = store.RoleUser
			}
			u = &store.User{
				ID: secrets.RandomID(), Email: email, Name: firstNonEmpty(claims.Name, claims.PreferredUser),
				Role: role, Provider: provKey, ProviderSubject: idTok.Subject,
				RecoveryCodes: store.StringList{}, CreatedAt: store.Now(),
			}
			if err := m.st.CreateUser(ctx, u); err != nil {
				return nil, "", err
			}
			m.core.Audit(ctx, core.Actor{ID: u.ID, Name: u.Email, IP: ip}, "user.create", "user", u.ID, u.Email, map[string]string{"via": p.Name})
			m.core.Bus.Publish(core.EvUserCreated, map[string]string{"id": u.ID, "email": u.Email})
		default:
			return nil, "", err
		}
	} else if err != nil {
		return nil, "", err
	}
	if u.Disabled {
		return nil, "", ErrDisabled
	}
	if p.SyncGroups && p.GroupsClaim != "" {
		m.syncGroups(ctx, u, provKey, all[p.GroupsClaim])
	}
	res, err := m.startSession(ctx, u, ip, ua, "sso:"+p.Name)
	return res, st.ReturnTo, err
}

func subtleNonce(s string) string {
	h := sha256.Sum256([]byte(s))
	return base64.RawStdEncoding.EncodeToString(h[:])
}

// syncGroups mirrors IdP group membership into Gorget groups with source=provKey.
func (m *Manager) syncGroups(ctx context.Context, u *store.User, provKey string, claim any) {
	var names []string
	switch v := claim.(type) {
	case []any:
		for _, x := range v {
			if s, ok := x.(string); ok {
				names = append(names, s)
			}
		}
	case string:
		names = strings.Split(v, ",")
	}
	var keep []string
	for _, raw := range names {
		name := core.SanitizeName(strings.TrimPrefix(raw, "/"))
		if name == "" {
			continue
		}
		g, err := m.st.GetGroupByName(ctx, name)
		if errors.Is(err, store.ErrNotFound) {
			g = &store.Group{ID: secrets.RandomID(), Name: name, Description: "Synced from identity provider", Source: provKey, CreatedAt: store.Now()}
			if err := m.st.CreateGroup(ctx, g); err != nil {
				continue
			}
		} else if err != nil {
			continue
		}
		if err := m.st.AddGroupMember(ctx, g.ID, u.ID); err == nil {
			keep = append(keep, g.ID)
		}
	}
	_ = m.st.RemoveUserFromSourceGroups(ctx, u.ID, provKey, keep)
	m.core.Coord.Trigger()
}
