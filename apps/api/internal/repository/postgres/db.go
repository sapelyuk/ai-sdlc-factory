// Package repository provides PostgreSQL data access.
package repository

import (
	"context"

	"github.com/jackc/pgx/v5/pgxpool"
)

// NewPool creates a new pgxpool connection pool.
func NewPool(ctx context.Context, dsn string) (*pgxpool.Pool, error) {
	return pgxpool.New(ctx, dsn)
}
