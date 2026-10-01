// Package geoip maps public IP addresses to countries using a local MaxMind-format
// database file. Lookups never leave the server. The free DB-IP "IP to Country
// Lite" database (CC BY 4.0) can be downloaded automatically.
package geoip

import (
	"compress/gzip"
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/netip"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/oschwald/maxminddb-golang/v2"
)

// Attribution must be shown wherever countries from the DB-IP database appear.
const Attribution = "IP geolocation by DB-IP (db-ip.com), CC BY 4.0"

// DB is a hot-swappable country database. The zero value (no file) answers "".
type DB struct {
	dir string

	mu     sync.RWMutex
	r      *maxminddb.Reader
	path   string
	loaded time.Time
}

// Status describes the loaded database for the console.
type Status struct {
	Loaded   bool   `json:"loaded"`
	File     string `json:"file,omitempty"`
	Type     string `json:"type,omitempty"`
	BuiltAt  int64  `json:"built_at,omitempty"`
	LoadedAt int64  `json:"loaded_at,omitempty"`
	Source   string `json:"source,omitempty"`
}

// Open loads the newest database file in dir (if any). A missing file is not an
// error: countries are then unknown until one is downloaded.
func Open(dir string) *DB {
	db := &DB{dir: dir}
	_ = db.loadNewest()
	return db
}

func (db *DB) loadNewest() error {
	matches, _ := filepath.Glob(filepath.Join(db.dir, "*.mmdb"))
	if len(matches) == 0 {
		return os.ErrNotExist
	}
	sort.Strings(matches) // file names carry the date, so the last one is newest
	return db.load(matches[len(matches)-1])
}

func (db *DB) load(path string) error {
	r, err := maxminddb.Open(path)
	if err != nil {
		return err
	}
	if err := r.Verify(); err != nil {
		r.Close()
		return fmt.Errorf("country database %s is damaged: %w", path, err)
	}
	db.mu.Lock()
	old, oldPath := db.r, db.path
	db.r, db.path, db.loaded = r, path, time.Now()
	db.mu.Unlock()
	if old != nil {
		old.Close()
		if oldPath != path {
			_ = os.Remove(oldPath)
		}
	}
	return nil
}

// Country returns the ISO 3166 code (e.g. "DE") for ip, or "" when unknown.
func (db *DB) Country(ip string) string {
	if db == nil {
		return ""
	}
	a, err := netip.ParseAddr(strings.TrimSpace(ip))
	if err != nil || !a.IsGlobalUnicast() || a.IsPrivate() {
		return ""
	}
	db.mu.RLock()
	defer db.mu.RUnlock()
	if db.r == nil {
		return ""
	}
	var rec struct {
		Country struct {
			ISOCode string `maxminddb:"iso_code"`
		} `maxminddb:"country"`
	}
	if err := db.r.Lookup(a.Unmap()).Decode(&rec); err != nil {
		return ""
	}
	return strings.ToUpper(rec.Country.ISOCode)
}

// Loaded reports whether a database is available.
func (db *DB) Loaded() bool {
	if db == nil {
		return false
	}
	db.mu.RLock()
	defer db.mu.RUnlock()
	return db.r != nil
}

func (db *DB) Status() Status {
	if db == nil {
		return Status{}
	}
	db.mu.RLock()
	defer db.mu.RUnlock()
	if db.r == nil {
		return Status{}
	}
	md := db.r.Metadata
	st := Status{Loaded: true, File: filepath.Base(db.path), Type: md.DatabaseType, BuiltAt: int64(md.BuildEpoch), LoadedAt: db.loaded.Unix()}
	if strings.Contains(strings.ToLower(db.path), "dbip") {
		st.Source = Attribution
	}
	return st
}

// Age is how old the loaded database is (0 when none is loaded).
func (db *DB) Age() time.Duration {
	st := db.Status()
	if !st.Loaded || st.BuiltAt == 0 {
		return 0
	}
	return time.Since(time.Unix(st.BuiltAt, 0))
}

// downloadURL is DB-IP's free monthly country database.
func downloadURL(t time.Time) string {
	return fmt.Sprintf("https://download.db-ip.com/free/dbip-country-lite-%s.mmdb.gz", t.UTC().Format("2006-01"))
}

// Download fetches the latest free country database and switches to it. DB-IP
// publishes at the start of each month, so the previous month is tried too.
func (db *DB) Download(ctx context.Context, hc *http.Client) error {
	if hc == nil {
		hc = &http.Client{Timeout: 3 * time.Minute}
	}
	if err := os.MkdirAll(db.dir, 0o700); err != nil {
		return err
	}
	now := time.Now().UTC()
	var lastErr error
	for _, t := range []time.Time{now, now.AddDate(0, -1, 0)} {
		path := filepath.Join(db.dir, "dbip-country-"+t.Format("2006-01")+".mmdb")
		if db.currentPath() == path {
			return nil // already have this month's file
		}
		if err := fetch(ctx, hc, downloadURL(t), path); err != nil {
			lastErr = err
			continue
		}
		if err := db.load(path); err != nil {
			_ = os.Remove(path)
			return err
		}
		return nil
	}
	return fmt.Errorf("download the country database: %w", lastErr)
}

func (db *DB) currentPath() string {
	db.mu.RLock()
	defer db.mu.RUnlock()
	return db.path
}

func fetch(ctx context.Context, hc *http.Client, url, dst string) error {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return err
	}
	req.Header.Set("User-Agent", "gorget-server")
	resp, err := hc.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("%s: HTTP %d", url, resp.StatusCode)
	}
	zr, err := gzip.NewReader(io.LimitReader(resp.Body, 256<<20))
	if err != nil {
		return err
	}
	defer zr.Close()
	tmp := dst + ".part"
	f, err := os.OpenFile(tmp, os.O_CREATE|os.O_TRUNC|os.O_WRONLY, 0o600)
	if err != nil {
		return err
	}
	n, err := io.Copy(f, io.LimitReader(zr, 512<<20))
	if cerr := f.Close(); err == nil {
		err = cerr
	}
	if err == nil && n < 1<<20 {
		err = errors.New("the downloaded file is too small to be a country database")
	}
	if err != nil {
		_ = os.Remove(tmp)
		return err
	}
	return os.Rename(tmp, dst)
}

// Close releases the database.
func (db *DB) Close() {
	if db == nil {
		return
	}
	db.mu.Lock()
	defer db.mu.Unlock()
	if db.r != nil {
		db.r.Close()
		db.r = nil
	}
}
