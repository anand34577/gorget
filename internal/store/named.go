package store

import (
	"context"
	"database/sql"

	"github.com/jmoiron/sqlx"
)

func sqlxNamedExec(ctx context.Context, x q, query string, arg any) (sql.Result, error) {
	return sqlx.NamedExecContext(ctx, x, query, arg)
}
