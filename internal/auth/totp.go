package auth

import (
	"context"
	"errors"
	"slices"
	"strings"
	"time"

	"github.com/pquerna/otp"
	"github.com/pquerna/otp/totp"

	"github.com/anand34577/gorget/internal/secrets"
	"github.com/anand34577/gorget/internal/store"
)

// TOTPEnrollment is returned when starting TOTP setup.
type TOTPEnrollment struct {
	Secret string `json:"secret"`
	URL    string `json:"url"`
}

// BeginTOTP generates a new secret (stored sealed, not yet enabled).
func (m *Manager) BeginTOTP(ctx context.Context, u *store.User) (*TOTPEnrollment, error) {
	if u.TOTPEnabled {
		return nil, errors.New("authenticator app is already enabled")
	}
	key, err := totp.Generate(totp.GenerateOpts{
		Issuer:      "Gorget (" + m.core.Settings().Network.Name + ")",
		AccountName: u.Email,
		Algorithm:   otp.AlgorithmSHA1,
		Digits:      otp.DigitsSix,
		Period:      30,
	})
	if err != nil {
		return nil, err
	}
	sealed, err := m.core.Box.Seal(key.Secret())
	if err != nil {
		return nil, err
	}
	u.TOTPSecret = sealed
	if err := m.st.UpdateUser(ctx, u); err != nil {
		return nil, err
	}
	return &TOTPEnrollment{Secret: key.Secret(), URL: key.URL()}, nil
}

// ConfirmTOTP enables TOTP after verifying a code; returns one-time recovery codes.
func (m *Manager) ConfirmTOTP(ctx context.Context, u *store.User, code string) ([]string, error) {
	if u.TOTPEnabled || u.TOTPSecret == "" {
		return nil, errors.New("start authenticator setup first")
	}
	secret, err := m.core.Box.Open(u.TOTPSecret)
	if err != nil {
		return nil, err
	}
	if !validTOTP(secret, code) {
		return nil, ErrBadCode
	}
	codes, hashes := newRecoveryCodes()
	u.TOTPEnabled = true
	u.RecoveryCodes = hashes
	if err := m.st.UpdateUser(ctx, u); err != nil {
		return nil, err
	}
	return codes, nil
}

func (m *Manager) DisableTOTP(ctx context.Context, u *store.User) error {
	u.TOTPEnabled = false
	u.TOTPSecret = ""
	u.RecoveryCodes = nil
	return m.st.UpdateUser(ctx, u)
}

// VerifyMFA completes a pending session using a TOTP or recovery code.
func (m *Manager) VerifyMFA(ctx context.Context, se *store.Session, u *store.User, code, ip string) error {
	if !se.MFAPending {
		return nil
	}
	code = strings.ReplaceAll(strings.TrimSpace(code), " ", "")
	ok := false
	if u.TOTPEnabled {
		if secret, err := m.core.Box.Open(u.TOTPSecret); err == nil && validTOTP(secret, code) {
			ok = true
		}
	}
	if !ok && len(code) > 6 {
		h := secrets.HashToken(strings.ToLower(code))
		if i := slices.Index(u.RecoveryCodes, h); i >= 0 {
			u.RecoveryCodes = slices.Delete(u.RecoveryCodes, i, i+1)
			ok = true
		}
	}
	if !ok {
		u.FailedLogins++
		_ = m.st.UpdateUser(ctx, u)
		if u.FailedLogins >= 10 {
			_ = m.st.DeleteSession(ctx, se.ID)
		}
		return ErrBadCode
	}
	return m.finishMFA(ctx, se, u, ip, "totp")
}

func (m *Manager) finishMFA(ctx context.Context, se *store.Session, u *store.User, ip, method string) error {
	se.MFAPending = false
	now := store.Now()
	se.ExpiresAt = now + int64(m.core.Cfg.Security.SessionTTL.Seconds())
	se.ReauthAt = now
	if err := m.st.UpdateSession(ctx, se); err != nil {
		return err
	}
	m.completeLogin(ctx, u, ip, "password+"+method)
	return nil
}

func validTOTP(secret, code string) bool {
	ok, err := totp.ValidateCustom(code, secret, time.Now().UTC(), totp.ValidateOpts{
		Period: 30, Skew: 1, Digits: otp.DigitsSix, Algorithm: otp.AlgorithmSHA1,
	})
	return err == nil && ok
}

func newRecoveryCodes() (codes []string, hashes []string) {
	for i := 0; i < 10; i++ {
		raw := strings.ToLower(secrets.RandomToken("", 8))
		raw = strings.NewReplacer("-", "x", "_", "y").Replace(raw)
		code := raw[:5] + "-" + raw[5:10]
		codes = append(codes, code)
		hashes = append(hashes, secrets.HashToken(strings.ReplaceAll(code, " ", "")))
	}
	return codes, hashes
}
