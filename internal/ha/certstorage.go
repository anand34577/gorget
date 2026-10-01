package ha

import (
	"context"
	"errors"
	"io/fs"
	"path"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/caddyserver/certmagic"

	"github.com/anand34577/gorget/internal/secrets"
	"github.com/anand34577/gorget/internal/store"
)

// CertStorage keeps TLS certificates, ACME accounts and challenge state in the shared
// database so every instance serves the same certificates and only one instance at a
// time talks to the certificate authority.
type CertStorage struct {
	st    *store.Store
	owner string

	mu    sync.Mutex
	locks map[string]string // lock name -> owner token
}

var _ certmagic.Storage = (*CertStorage)(nil)

func NewCertStorage(st *store.Store, instanceID string) *CertStorage {
	return &CertStorage{st: st, owner: instanceID, locks: map[string]string{}}
}

const lockTTLSeconds = 15 * 60 // issuance can take a while; locks of crashed instances expire

// Lock blocks until the named lock is ours (certmagic uses it around issuance and renewal).
func (s *CertStorage) Lock(ctx context.Context, name string) error {
	token := s.owner + "/" + secrets.RandomToken("", 6)
	for {
		ok, err := s.st.TryLock(ctx, "cert:"+name, token, lockTTLSeconds)
		if err != nil {
			return err
		}
		if ok {
			s.mu.Lock()
			s.locks[name] = token
			s.mu.Unlock()
			return nil
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(time.Second):
		}
	}
}

func (s *CertStorage) Unlock(ctx context.Context, name string) error {
	s.mu.Lock()
	token := s.locks[name]
	delete(s.locks, name)
	s.mu.Unlock()
	if token == "" {
		return nil
	}
	return s.st.Unlock(ctx, "cert:"+name, token)
}

func (s *CertStorage) Store(ctx context.Context, key string, value []byte) error {
	return s.st.KVPut(ctx, clean(key), value)
}

func (s *CertStorage) Load(ctx context.Context, key string) ([]byte, error) {
	kv, err := s.st.KVGet(ctx, clean(key))
	if errors.Is(err, store.ErrNotFound) {
		return nil, fs.ErrNotExist
	}
	if err != nil {
		return nil, err
	}
	return kv.Value, nil
}

func (s *CertStorage) Delete(ctx context.Context, key string) error {
	key = clean(key)
	// Deleting a "directory" removes everything below it, like a file system.
	keys, err := s.st.KVList(ctx, key+"/")
	if err != nil {
		return err
	}
	for _, k := range keys {
		if err := s.st.KVDelete(ctx, k); err != nil {
			return err
		}
	}
	return s.st.KVDelete(ctx, key)
}

func (s *CertStorage) Exists(ctx context.Context, key string) bool {
	_, err := s.Stat(ctx, key)
	return err == nil
}

// List returns the keys below path: direct children, or every descendant when recursive.
func (s *CertStorage) List(ctx context.Context, p string, recursive bool) ([]string, error) {
	p = clean(p)
	prefix := p + "/"
	if p == "" {
		prefix = ""
	}
	keys, err := s.st.KVList(ctx, prefix)
	if err != nil {
		return nil, err
	}
	if len(keys) == 0 {
		return nil, fs.ErrNotExist
	}
	if recursive {
		return keys, nil
	}
	seen := map[string]bool{}
	for _, k := range keys {
		rest := strings.TrimPrefix(k, prefix)
		first, _, _ := strings.Cut(rest, "/")
		seen[prefix+first] = true
	}
	out := make([]string, 0, len(seen))
	for k := range seen {
		out = append(out, k)
	}
	sort.Strings(out)
	return out, nil
}

func (s *CertStorage) Stat(ctx context.Context, key string) (certmagic.KeyInfo, error) {
	key = clean(key)
	kv, err := s.st.KVGet(ctx, key)
	if err == nil {
		return certmagic.KeyInfo{Key: key, Modified: time.Unix(kv.Modified, 0), Size: int64(len(kv.Value)), IsTerminal: true}, nil
	}
	if !errors.Is(err, store.ErrNotFound) {
		return certmagic.KeyInfo{}, err
	}
	// Not a blob: a directory if anything lives below it.
	below, err := s.st.KVList(ctx, key+"/")
	if err != nil {
		return certmagic.KeyInfo{}, err
	}
	if len(below) == 0 {
		return certmagic.KeyInfo{}, fs.ErrNotExist
	}
	return certmagic.KeyInfo{Key: key, Modified: time.Now(), IsTerminal: false}, nil
}

func clean(key string) string { return strings.Trim(path.Clean("/"+key), "/") }
