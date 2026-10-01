// Package backup creates encrypted, database-independent backups and migrates
// data between SQLite and PostgreSQL.
//
// A backup is a JSON document of every table, encrypted with a key derived from
// the master key. Restoring therefore needs the same master key — back it up
// separately and keep it safe.
package backup

import (
	"bytes"
	"compress/gzip"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"golang.org/x/crypto/chacha20poly1305"

	"github.com/anand34577/gorget/internal/secrets"
	"github.com/anand34577/gorget/internal/store"
)

const magic = "GORGETBK1"

type dump struct {
	Version   int                         `json:"version"`
	CreatedAt time.Time                   `json:"created_at"`
	Schema    int                         `json:"schema"`
	Tables    map[string][]map[string]any `json:"tables"`
}

// Export reads every table into memory.
func Export(ctx context.Context, st *store.Store) (*dump, error) {
	d := &dump{Version: 1, CreatedAt: time.Now().UTC(), Tables: map[string][]map[string]any{}}
	if err := st.DB().GetContext(ctx, &d.Schema, `SELECT COALESCE(MAX(version),0) FROM schema_migrations`); err != nil {
		return nil, err
	}
	for _, t := range store.Tables {
		rows, err := st.DB().QueryxContext(ctx, "SELECT * FROM "+t)
		if err != nil {
			return nil, fmt.Errorf("read %s: %w", t, err)
		}
		types, _ := rows.ColumnTypes()
		var out []map[string]any
		for rows.Next() {
			m := map[string]any{}
			if err := rows.MapScan(m); err != nil {
				rows.Close()
				return nil, err
			}
			for _, ct := range types {
				v := m[ct.Name()]
				if b, ok := v.([]byte); ok {
					v = string(b)
				}
				if isBool(ct.DatabaseTypeName()) {
					v = toBool(v)
				}
				m[ct.Name()] = v
			}
			out = append(out, m)
		}
		rows.Close()
		d.Tables[t] = out
	}
	return d, nil
}

func isBool(t string) bool {
	t = strings.ToUpper(t)
	return t == "BOOLEAN" || t == "BOOL"
}

func toBool(v any) bool {
	switch x := v.(type) {
	case bool:
		return x
	case int64:
		return x != 0
	case string:
		return x == "1" || strings.EqualFold(x, "true")
	}
	return false
}

// Import writes a dump into an empty database (same schema version).
func Import(ctx context.Context, st *store.Store, d *dump) error {
	var schema int
	if err := st.DB().GetContext(ctx, &schema, `SELECT COALESCE(MAX(version),0) FROM schema_migrations`); err != nil {
		return err
	}
	if d.Schema > schema {
		return fmt.Errorf("backup schema %d is newer than this server (%d)", d.Schema, schema)
	}
	var n int
	if err := st.DB().GetContext(ctx, &n, `SELECT COUNT(*) FROM users`); err != nil {
		return err
	}
	if n > 0 {
		return errors.New("target database is not empty; restore into a fresh database")
	}
	tx, err := st.DB().BeginTxx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	// settings/policy may have been created on first start; replace them.
	for _, t := range []string{"settings", "policy_versions", "devices"} {
		if _, err := tx.ExecContext(ctx, "DELETE FROM "+t); err != nil {
			return err
		}
	}
	for _, t := range store.Tables {
		for _, row := range d.Tables[t] {
			cols := make([]string, 0, len(row))
			for c := range row {
				cols = append(cols, c)
			}
			sort.Strings(cols)
			args := make([]any, len(cols))
			ph := make([]string, len(cols))
			for i, c := range cols {
				args[i] = normalise(row[c])
				ph[i] = "?"
			}
			q := tx.Rebind(fmt.Sprintf("INSERT INTO %s (%s) VALUES (%s)", t, strings.Join(cols, ", "), strings.Join(ph, ", ")))
			if _, err := tx.ExecContext(ctx, q, args...); err != nil {
				return fmt.Errorf("restore %s: %w", t, err)
			}
		}
	}
	return tx.Commit()
}

func normalise(v any) any {
	switch x := v.(type) {
	case json.Number:
		if i, err := x.Int64(); err == nil {
			return i
		}
		f, _ := x.Float64()
		return f
	case float64:
		return int64(x)
	}
	return v
}

// Write encrypts and writes a dump to path.
func Write(d *dump, box *secrets.Box, path string) error {
	var buf bytes.Buffer
	zw := gzip.NewWriter(&buf)
	if err := json.NewEncoder(zw).Encode(d); err != nil {
		return err
	}
	if err := zw.Close(); err != nil {
		return err
	}
	aead, err := chacha20poly1305.NewX(box.DeriveKey("backup"))
	if err != nil {
		return err
	}
	nonce := secrets.RandomBytes(aead.NonceSize())
	out := append([]byte(magic), nonce...)
	out = aead.Seal(out, nonce, buf.Bytes(), []byte(magic))
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return err
	}
	tmp := path + ".tmp"
	if err := os.WriteFile(tmp, out, 0o600); err != nil {
		return err
	}
	return os.Rename(tmp, path)
}

// Read decrypts a backup file.
func Read(path string, box *secrets.Box) (*dump, error) {
	raw, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	aead, err := chacha20poly1305.NewX(box.DeriveKey("backup"))
	if err != nil {
		return nil, err
	}
	if len(raw) < len(magic)+aead.NonceSize() || string(raw[:len(magic)]) != magic {
		return nil, errors.New("not a Gorget backup file")
	}
	nonce := raw[len(magic) : len(magic)+aead.NonceSize()]
	pt, err := aead.Open(nil, nonce, raw[len(magic)+aead.NonceSize():], []byte(magic))
	if err != nil {
		return nil, errors.New("cannot decrypt backup (wrong master key?)")
	}
	zr, err := gzip.NewReader(bytes.NewReader(pt))
	if err != nil {
		return nil, err
	}
	dec := json.NewDecoder(io.LimitReader(zr, 2<<30))
	dec.UseNumber()
	var d dump
	if err := dec.Decode(&d); err != nil {
		return nil, err
	}
	return &d, nil
}

// Scheduler writes periodic backups and keeps the newest `keep` files.
func Scheduler(ctx context.Context, st *store.Store, box *secrets.Box, dir string, every time.Duration, keep int, log *slog.Logger) {
	if every <= 0 {
		return
	}
	t := time.NewTicker(every)
	defer t.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-t.C:
		}
		path := filepath.Join(dir, "gorget-"+time.Now().UTC().Format("20060102-150405")+".gbk")
		d, err := Export(ctx, st)
		if err == nil {
			err = Write(d, box, path)
		}
		if err != nil {
			log.Error("scheduled backup failed", "err", err)
			continue
		}
		log.Info("backup written", "file", path)
		prune(dir, keep)
	}
}

func prune(dir string, keep int) {
	matches, _ := filepath.Glob(filepath.Join(dir, "gorget-*.gbk"))
	sort.Strings(matches)
	for len(matches) > keep {
		_ = os.Remove(matches[0])
		matches = matches[1:]
	}
}
