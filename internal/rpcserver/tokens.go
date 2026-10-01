package rpcserver

import (
	"crypto/hmac"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"errors"
	"strings"
	"time"
)

// Device session tokens are stateless and HMAC-signed; revocation is enforced by
// re-checking the device's state on every request.
const tokenPrefix = "gd_"

type tokenClaims struct {
	DeviceID   string `json:"d"`
	MachineKey string `json:"m"`
	Expires    int64  `json:"e"`
}

type tokenSigner struct{ key []byte }

func (t tokenSigner) issue(deviceID, machineKey string, ttl time.Duration) (string, int64) {
	exp := time.Now().Add(ttl).Unix()
	payload, _ := json.Marshal(tokenClaims{DeviceID: deviceID, MachineKey: machineKey, Expires: exp})
	p := base64.RawURLEncoding.EncodeToString(payload)
	return tokenPrefix + p + "." + t.sign(p), exp
}

func (t tokenSigner) sign(p string) string {
	m := hmac.New(sha256.New, t.key)
	m.Write([]byte(p))
	return base64.RawURLEncoding.EncodeToString(m.Sum(nil))
}

func (t tokenSigner) verify(tok string) (*tokenClaims, error) {
	if !strings.HasPrefix(tok, tokenPrefix) {
		return nil, errors.New("malformed token")
	}
	p, sig, ok := strings.Cut(strings.TrimPrefix(tok, tokenPrefix), ".")
	if !ok || !hmac.Equal([]byte(sig), []byte(t.sign(p))) {
		return nil, errors.New("invalid token")
	}
	raw, err := base64.RawURLEncoding.DecodeString(p)
	if err != nil {
		return nil, err
	}
	var c tokenClaims
	if err := json.Unmarshal(raw, &c); err != nil {
		return nil, err
	}
	if time.Now().Unix() > c.Expires {
		return nil, errors.New("token expired")
	}
	return &c, nil
}
