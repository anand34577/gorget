package store

import (
	"context"
	"fmt"
	"reflect"
	"strings"
	"sync"
)

var colCache sync.Map // reflect.Type -> []string

func columnsOf(v any) []string {
	t := reflect.TypeOf(v)
	for t.Kind() == reflect.Pointer {
		t = t.Elem()
	}
	if c, ok := colCache.Load(t); ok {
		return c.([]string)
	}
	var cols []string
	for i := 0; i < t.NumField(); i++ {
		tag := t.Field(i).Tag.Get("db")
		if tag == "" || tag == "-" {
			continue
		}
		cols = append(cols, tag)
	}
	colCache.Store(t, cols)
	return cols
}

func (s *Store) insert(ctx context.Context, x q, table string, v any) error {
	cols := columnsOf(v)
	query := fmt.Sprintf("INSERT INTO %s (%s) VALUES (:%s)", table, strings.Join(cols, ", "), strings.Join(cols, ", :"))
	_, err := sqlxNamedExec(ctx, x, query, v)
	if err != nil && isUniqueViolation(err) {
		return fmt.Errorf("%w: %v", ErrConflict, err)
	}
	return err
}

// update writes every column of v (except the key) to the row identified by key.
func (s *Store) update(ctx context.Context, x q, table, key string, v any) error {
	cols := columnsOf(v)
	sets := make([]string, 0, len(cols))
	for _, c := range cols {
		if c != key {
			sets = append(sets, c+" = :"+c)
		}
	}
	query := fmt.Sprintf("UPDATE %s SET %s WHERE %s = :%s", table, strings.Join(sets, ", "), key, key)
	res, err := sqlxNamedExec(ctx, x, query, v)
	if err != nil {
		if isUniqueViolation(err) {
			return fmt.Errorf("%w: %v", ErrConflict, err)
		}
		return err
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return ErrNotFound
	}
	return nil
}
