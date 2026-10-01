package auth

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/url"
	"time"

	"github.com/go-webauthn/webauthn/protocol"
	"github.com/go-webauthn/webauthn/webauthn"

	"github.com/anand34577/gorget/internal/secrets"
	"github.com/anand34577/gorget/internal/store"
)

type waUser struct {
	u     *store.User
	creds []webauthn.Credential
}

func (w *waUser) WebAuthnID() []byte                         { return []byte(w.u.ID) }
func (w *waUser) WebAuthnName() string                       { return w.u.Email }
func (w *waUser) WebAuthnDisplayName() string                { return firstNonEmpty(w.u.Name, w.u.Email) }
func (w *waUser) WebAuthnCredentials() []webauthn.Credential { return w.creds }

func firstNonEmpty(a, b string) string {
	if a != "" {
		return a
	}
	return b
}

func (m *Manager) webAuthn() (*webauthn.WebAuthn, error) {
	u, err := url.Parse(m.core.Cfg.PublicURL)
	if err != nil {
		return nil, err
	}
	return webauthn.New(&webauthn.Config{
		RPDisplayName: "Gorget",
		RPID:          u.Hostname(),
		RPOrigins:     []string{u.Scheme + "://" + u.Host},
		AuthenticatorSelection: protocol.AuthenticatorSelection{
			UserVerification: protocol.VerificationPreferred,
			ResidentKey:      protocol.ResidentKeyRequirementPreferred,
		},
		Timeouts: webauthn.TimeoutsConfig{
			Login:        webauthn.TimeoutConfig{Enforce: true, Timeout: 2 * time.Minute},
			Registration: webauthn.TimeoutConfig{Enforce: true, Timeout: 5 * time.Minute},
		},
	})
}

func (m *Manager) loadWAUser(ctx context.Context, u *store.User) (*waUser, []store.WebAuthnCredential, error) {
	rows, err := m.st.ListWebAuthnCredentials(ctx, u.ID)
	if err != nil {
		return nil, nil, err
	}
	wu := &waUser{u: u}
	for _, r := range rows {
		var c webauthn.Credential
		if err := json.Unmarshal([]byte(r.Credential), &c); err == nil {
			wu.creds = append(wu.creds, c)
		}
	}
	return wu, rows, nil
}

// BeginPasskeyRegistration starts registering a new passkey / security key.
func (m *Manager) BeginPasskeyRegistration(ctx context.Context, u *store.User) (*protocol.CredentialCreation, error) {
	w, err := m.webAuthn()
	if err != nil {
		return nil, err
	}
	wu, _, err := m.loadWAUser(ctx, u)
	if err != nil {
		return nil, err
	}
	var exclude []protocol.CredentialDescriptor
	for _, c := range wu.creds {
		exclude = append(exclude, c.Descriptor())
	}
	opts, sess, err := w.BeginRegistration(wu, webauthn.WithExclusions(exclude))
	if err != nil {
		return nil, err
	}
	if err := m.putPending(ctx, "wa-reg:"+u.ID, sess, 5*time.Minute); err != nil {
		return nil, err
	}
	return opts, nil
}

func (m *Manager) FinishPasskeyRegistration(ctx context.Context, u *store.User, name string, r *http.Request) (*store.WebAuthnCredential, error) {
	var sd webauthn.SessionData
	if !m.takePending(ctx, "wa-reg:"+u.ID, &sd) {
		return nil, errors.New("registration expired; please try again")
	}
	w, err := m.webAuthn()
	if err != nil {
		return nil, err
	}
	wu, _, err := m.loadWAUser(ctx, u)
	if err != nil {
		return nil, err
	}
	cred, err := w.FinishRegistration(wu, sd, r)
	if err != nil {
		return nil, errors.New("passkey verification failed")
	}
	b, err := json.Marshal(cred)
	if err != nil {
		return nil, err
	}
	if name == "" {
		name = "Passkey"
	}
	row := &store.WebAuthnCredential{ID: secrets.RandomID(), UserID: u.ID, Name: truncate(name, 64), Credential: string(b), CreatedAt: store.Now()}
	return row, m.st.AddWebAuthnCredential(ctx, row)
}

// BeginPasskeyLogin starts the second-factor assertion for a pending session.
func (m *Manager) BeginPasskeyLogin(ctx context.Context, se *store.Session, u *store.User) (*protocol.CredentialAssertion, error) {
	w, err := m.webAuthn()
	if err != nil {
		return nil, err
	}
	wu, _, err := m.loadWAUser(ctx, u)
	if err != nil {
		return nil, err
	}
	if len(wu.creds) == 0 {
		return nil, errors.New("no passkeys registered")
	}
	opts, sess, err := w.BeginLogin(wu)
	if err != nil {
		return nil, err
	}
	if err := m.putPending(ctx, "wa-login:"+se.ID, sess, 2*time.Minute); err != nil {
		return nil, err
	}
	return opts, nil
}

func (m *Manager) FinishPasskeyLogin(ctx context.Context, se *store.Session, u *store.User, r *http.Request, ip string) error {
	var sd webauthn.SessionData
	if !m.takePending(ctx, "wa-login:"+se.ID, &sd) {
		return errors.New("sign-in expired; please try again")
	}
	w, err := m.webAuthn()
	if err != nil {
		return err
	}
	wu, rows, err := m.loadWAUser(ctx, u)
	if err != nil {
		return err
	}
	cred, err := w.FinishLogin(wu, sd, r)
	if err != nil {
		return ErrBadCode
	}
	// Persist the updated sign counter.
	for i := range rows {
		var c webauthn.Credential
		if json.Unmarshal([]byte(rows[i].Credential), &c) == nil && string(c.ID) == string(cred.ID) {
			b, _ := json.Marshal(cred)
			rows[i].Credential = string(b)
			rows[i].LastUsedAt = store.Now()
			_ = m.st.UpdateWebAuthnCredential(ctx, &rows[i])
		}
	}
	return m.finishMFA(ctx, se, u, ip, "passkey")
}
