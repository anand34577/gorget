package core

import (
	"context"
	"errors"

	"github.com/anand34577/gorget/internal/secrets"
	"github.com/anand34577/gorget/internal/store"
)

const keySCIM = "scim"

// SCIMSettings control the SCIM 2.0 provisioning endpoint (/scim/v2). Only a hash of the
// bearer token is stored.
type SCIMSettings struct {
	Enabled   bool   `json:"enabled"`
	TokenHash string `json:"token_hash"`
	CreatedAt int64  `json:"created_at"`
}

// SCIM returns the current provisioning settings (zero value when never configured).
func (c *Core) SCIM(ctx context.Context) SCIMSettings {
	var s SCIMSettings
	if err := c.Store.GetSetting(ctx, keySCIM, &s); err != nil && !errors.Is(err, store.ErrNotFound) {
		c.Log.Warn("read SCIM settings", "err", err)
	}
	return s
}

// RotateSCIMToken creates a new bearer token (shown once) and turns SCIM on.
func (c *Core) RotateSCIMToken(ctx context.Context) (string, error) {
	tok := secrets.RandomToken("gscim_", 32)
	s := SCIMSettings{Enabled: true, TokenHash: secrets.HashToken(tok), CreatedAt: store.Now()}
	if err := c.Store.PutSetting(ctx, keySCIM, s); err != nil {
		return "", err
	}
	return tok, nil
}

// DisableSCIM turns provisioning off and forgets the token.
func (c *Core) DisableSCIM(ctx context.Context) error {
	return c.Store.PutSetting(ctx, keySCIM, SCIMSettings{})
}

// CheckSCIMToken reports whether tok is the current SCIM token.
func (c *Core) CheckSCIMToken(ctx context.Context, tok string) bool {
	s := c.SCIM(ctx)
	return s.Enabled && s.TokenHash != "" && tok != "" && secrets.ConstantTimeEqual(secrets.HashToken(tok), s.TokenHash)
}
