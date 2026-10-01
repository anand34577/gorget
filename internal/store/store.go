// Package store is the persistence layer. It supports SQLite (default) and
// PostgreSQL through a single portable schema.
package store

import (
	"context"
	"database/sql"
	"database/sql/driver"
	"encoding/json"
	"errors"
	"fmt"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"time"

	_ "github.com/jackc/pgx/v5/stdlib"
	"github.com/jmoiron/sqlx"
	_ "modernc.org/sqlite"
)

var ErrNotFound = errors.New("not found")
var ErrConflict = errors.New("already exists")

type Store struct {
	db     *sqlx.DB
	driver string
}

// Open connects and runs pending migrations.
func Open(ctx context.Context, driverName, dsn string, maxOpen int) (*Store, error) {
	var db *sqlx.DB
	var err error
	switch driverName {
	case "sqlite":
		if err := os.MkdirAll(filepath.Dir(dsn), 0o700); err != nil {
			return nil, err
		}
		q := url.Values{}
		q.Add("_pragma", "busy_timeout(10000)")
		q.Add("_pragma", "journal_mode(WAL)")
		q.Add("_pragma", "foreign_keys(1)")
		q.Add("_pragma", "synchronous(NORMAL)")
		q.Add("_txlock", "immediate")
		db, err = sqlx.Open("sqlite", "file:"+filepath.ToSlash(dsn)+"?"+q.Encode())
		if err == nil {
			// SQLite allows a single writer; a small pool avoids lock contention.
			db.SetMaxOpenConns(4)
		}
	case "postgres":
		db, err = sqlx.Open("pgx", dsn)
		if err == nil {
			if maxOpen <= 0 {
				maxOpen = 20
			}
			db.SetMaxOpenConns(maxOpen)
			db.SetMaxIdleConns(maxOpen / 2)
			db.SetConnMaxLifetime(30 * time.Minute)
		}
	default:
		return nil, fmt.Errorf("unsupported database driver %q", driverName)
	}
	if err != nil {
		return nil, err
	}
	db = db.Unsafe()
	pingCtx, cancel := context.WithTimeout(ctx, 15*time.Second)
	defer cancel()
	if err := db.PingContext(pingCtx); err != nil {
		db.Close()
		return nil, fmt.Errorf("connect to %s: %w", driverName, err)
	}
	s := &Store{db: sqlx.NewDb(db.DB, driverNameFor(driverName)).Unsafe(), driver: driverName}
	if err := s.migrate(ctx); err != nil {
		db.Close()
		return nil, err
	}
	return s, nil
}

func driverNameFor(d string) string {
	if d == "postgres" {
		return "pgx"
	}
	return "sqlite3" // only used by sqlx to pick "?" bind vars
}

func (s *Store) Close() error   { return s.db.Close() }
func (s *Store) Driver() string { return s.driver }
func (s *Store) DB() *sqlx.DB   { return s.db }

func (s *Store) Ping(ctx context.Context) error { return s.db.PingContext(ctx) }

// q is the common interface of *sqlx.DB and *sqlx.Tx.
type q interface {
	sqlx.ExtContext
	GetContext(ctx context.Context, dest any, query string, args ...any) error
	SelectContext(ctx context.Context, dest any, query string, args ...any) error
}

// Tx runs fn in a transaction.
func (s *Store) Tx(ctx context.Context, fn func(tx *sqlx.Tx) error) error {
	tx, err := s.db.BeginTxx(ctx, nil)
	if err != nil {
		return err
	}
	if err := fn(tx); err != nil {
		_ = tx.Rollback()
		return err
	}
	return tx.Commit()
}

func (s *Store) rb(query string) string { return s.db.Rebind(query) }

func (s *Store) get(ctx context.Context, x q, dest any, query string, args ...any) error {
	err := x.GetContext(ctx, dest, s.rb(query), args...)
	if errors.Is(err, sql.ErrNoRows) {
		return ErrNotFound
	}
	return err
}

func (s *Store) sel(ctx context.Context, x q, dest any, query string, args ...any) error {
	return x.SelectContext(ctx, dest, s.rb(query), args...)
}

func (s *Store) exec(ctx context.Context, x q, query string, args ...any) (sql.Result, error) {
	res, err := x.ExecContext(ctx, s.rb(query), args...)
	if err != nil && isUniqueViolation(err) {
		return nil, fmt.Errorf("%w: %v", ErrConflict, err)
	}
	return res, err
}

func (s *Store) execOne(ctx context.Context, x q, query string, args ...any) error {
	res, err := s.exec(ctx, x, query, args...)
	if err != nil {
		return err
	}
	n, err := res.RowsAffected()
	if err != nil {
		return err
	}
	if n == 0 {
		return ErrNotFound
	}
	return nil
}

func isUniqueViolation(err error) bool {
	m := strings.ToLower(err.Error())
	return strings.Contains(m, "unique constraint") || strings.Contains(m, "duplicate key") || strings.Contains(m, "sqlstate 23505")
}

// Now returns the current Unix time in seconds; all timestamps are stored this way.
func Now() int64 { return time.Now().Unix() }

// StringList is a []string stored as JSON text.
type StringList []string

func (l StringList) Value() (driver.Value, error) {
	if l == nil {
		return "[]", nil
	}
	b, err := json.Marshal([]string(l))
	return string(b), err
}

func (l *StringList) Scan(src any) error {
	var b []byte
	switch v := src.(type) {
	case nil:
		*l = nil
		return nil
	case string:
		b = []byte(v)
	case []byte:
		b = v
	default:
		return fmt.Errorf("StringList: unsupported type %T", src)
	}
	if len(b) == 0 {
		*l = nil
		return nil
	}
	return json.Unmarshal(b, (*[]string)(l))
}

// JSON stores any JSON-encodable value as text.
type JSON[T any] struct{ V T }

func (j JSON[T]) Value() (driver.Value, error) {
	b, err := json.Marshal(j.V)
	return string(b), err
}

func (j *JSON[T]) Scan(src any) error {
	var b []byte
	switch v := src.(type) {
	case nil:
		return nil
	case string:
		b = []byte(v)
	case []byte:
		b = v
	default:
		return fmt.Errorf("JSON: unsupported type %T", src)
	}
	if len(b) == 0 {
		return nil
	}
	return json.Unmarshal(b, &j.V)
}

func (j JSON[T]) MarshalJSON() ([]byte, error)  { return json.Marshal(j.V) }
func (j *JSON[T]) UnmarshalJSON(b []byte) error { return json.Unmarshal(b, &j.V) }
