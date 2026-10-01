// Package secrets provides envelope encryption for secrets at rest,
// password hashing and random token helpers.
package secrets

import (
	"crypto/rand"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/base64"
	"encoding/hex"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"golang.org/x/crypto/argon2"
	"golang.org/x/crypto/chacha20poly1305"
)

const sealedPrefix = "v1:"

// Box encrypts and decrypts small secrets with XChaCha20-Poly1305 under a 32-byte master key.
type Box struct {
	key []byte
}

func NewBox(key []byte) (*Box, error) {
	if len(key) != chacha20poly1305.KeySize {
		return nil, fmt.Errorf("master key must be %d bytes", chacha20poly1305.KeySize)
	}
	return &Box{key: append([]byte(nil), key...)}, nil
}

// LoadOrCreateMasterKey returns the key from b64 if set, else reads path,
// creating a new random key file (0600) if it does not exist.
func LoadOrCreateMasterKey(b64, path string) ([]byte, error) {
	if b64 != "" {
		k, err := base64.StdEncoding.DecodeString(strings.TrimSpace(b64))
		if err != nil {
			return nil, fmt.Errorf("master key: %w", err)
		}
		return k, nil
	}
	b, err := os.ReadFile(path)
	if err == nil {
		k, err := base64.StdEncoding.DecodeString(strings.TrimSpace(string(b)))
		if err != nil {
			return nil, fmt.Errorf("master key file %s: %w", path, err)
		}
		return k, nil
	}
	if !errors.Is(err, os.ErrNotExist) {
		return nil, err
	}
	k := RandomBytes(32)
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return nil, err
	}
	if err := os.WriteFile(path, []byte(base64.StdEncoding.EncodeToString(k)+"\n"), 0o600); err != nil {
		return nil, err
	}
	return k, nil
}

// DeriveKey derives a purpose-specific 32-byte key from the master key.
func (b *Box) DeriveKey(label string) []byte {
	h := sha256.New()
	h.Write([]byte("gorget-derive-v1|" + label + "|"))
	h.Write(b.key)
	return h.Sum(nil)
}

// Seal encrypts plaintext. Empty input yields empty output.
func (b *Box) Seal(plaintext string) (string, error) {
	if plaintext == "" {
		return "", nil
	}
	aead, err := chacha20poly1305.NewX(b.key)
	if err != nil {
		return "", err
	}
	nonce := RandomBytes(aead.NonceSize())
	ct := aead.Seal(nonce, nonce, []byte(plaintext), nil)
	return sealedPrefix + base64.RawStdEncoding.EncodeToString(ct), nil
}

// MustSeal panics on failure; only for values where failure is impossible.
func (b *Box) MustSeal(plaintext string) string {
	s, err := b.Seal(plaintext)
	if err != nil {
		panic(err)
	}
	return s
}

func (b *Box) Open(sealed string) (string, error) {
	if sealed == "" {
		return "", nil
	}
	if !strings.HasPrefix(sealed, sealedPrefix) {
		return "", errors.New("secret is not sealed")
	}
	raw, err := base64.RawStdEncoding.DecodeString(strings.TrimPrefix(sealed, sealedPrefix))
	if err != nil {
		return "", err
	}
	aead, err := chacha20poly1305.NewX(b.key)
	if err != nil {
		return "", err
	}
	if len(raw) < aead.NonceSize() {
		return "", errors.New("sealed secret too short")
	}
	pt, err := aead.Open(nil, raw[:aead.NonceSize()], raw[aead.NonceSize():], nil)
	if err != nil {
		return "", errors.New("failed to decrypt secret (wrong master key?)")
	}
	return string(pt), nil
}

func RandomBytes(n int) []byte {
	b := make([]byte, n)
	if _, err := rand.Read(b); err != nil {
		panic(err)
	}
	return b
}

// RandomToken returns a URL-safe random token with the given prefix, e.g. "gk_".
func RandomToken(prefix string, nbytes int) string {
	return prefix + base64.RawURLEncoding.EncodeToString(RandomBytes(nbytes))
}

// RandomID returns a 16-byte hex identifier.
func RandomID() string { return hex.EncodeToString(RandomBytes(16)) }

// HashToken returns a hex SHA-256 digest suitable for storing high-entropy tokens.
func HashToken(token string) string {
	h := sha256.Sum256([]byte(token))
	return hex.EncodeToString(h[:])
}

func ConstantTimeEqual(a, b string) bool {
	return subtle.ConstantTimeCompare([]byte(a), []byte(b)) == 1
}

// Argon2id parameters (OWASP 2024 recommendation: m=19MiB,t=2,p=1 minimum; we use more).
const (
	argonTime    = 3
	argonMemory  = 64 * 1024
	argonThreads = 2
	argonKeyLen  = 32
)

// HashPassword returns an encoded Argon2id hash.
func HashPassword(password string) string {
	salt := RandomBytes(16)
	key := argon2.IDKey([]byte(password), salt, argonTime, argonMemory, argonThreads, argonKeyLen)
	return fmt.Sprintf("$argon2id$v=19$m=%d,t=%d,p=%d$%s$%s", argonMemory, argonTime, argonThreads,
		base64.RawStdEncoding.EncodeToString(salt), base64.RawStdEncoding.EncodeToString(key))
}

// VerifyPassword checks a password against an encoded Argon2id hash.
func VerifyPassword(encoded, password string) bool {
	parts := strings.Split(encoded, "$")
	if len(parts) != 6 || parts[1] != "argon2id" {
		return false
	}
	var m, t uint32
	var p uint8
	if _, err := fmt.Sscanf(parts[3], "m=%d,t=%d,p=%d", &m, &t, &p); err != nil {
		return false
	}
	salt, err := base64.RawStdEncoding.DecodeString(parts[4])
	if err != nil {
		return false
	}
	want, err := base64.RawStdEncoding.DecodeString(parts[5])
	if err != nil {
		return false
	}
	got := argon2.IDKey([]byte(password), salt, t, m, p, uint32(len(want)))
	return subtle.ConstantTimeCompare(got, want) == 1
}

// dummyHash is used to equalise timing when a user does not exist.
var dummyHash = HashPassword("gorget-timing-equaliser")

// BurnPasswordCheck spends the same time as a real password verification.
func BurnPasswordCheck(password string) { VerifyPassword(dummyHash, password) }
