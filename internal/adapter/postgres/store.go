// Package postgres is the team server's ledger (architecture-patterns-v0
// §4.3): one append-only event table per tenant with a hash chain, a
// rfc6962 Merkle tree kept incrementally in merkle_node, and signed
// checkpoints. Ledger(org) implements port.Ledger for one org; every
// statement runs in a transaction with app.org_id set (RLS).
package postgres

import (
	"context"
	"database/sql"
	"embed"
	"errors"
	"fmt"

	_ "github.com/jackc/pgx/v5/stdlib" // database/sql driver "pgx"
	"github.com/pressly/goose/v3"

	"github.com/young1ll/keelage/internal/port"
)

//go:embed migrations/*.sql
var migrations embed.FS

// Store is the database handle.
type Store struct {
	db     *sql.DB
	signer port.Signer
}

// Open connects and migrates. signer (nil allowed) signs appended records.
func Open(ctx context.Context, dsn string, signer port.Signer) (*Store, error) {
	db, err := sql.Open("pgx", dsn)
	if err != nil {
		return nil, fmt.Errorf("postgres: dsn: %w", err)
	}
	if err := db.PingContext(ctx); err != nil {
		_ = db.Close()
		return nil, fmt.Errorf("postgres: connect: %w", err)
	}
	goose.SetBaseFS(migrations)
	goose.SetLogger(goose.NopLogger())
	if err := goose.SetDialect("postgres"); err != nil {
		_ = db.Close()
		return nil, err
	}
	if err := goose.UpContext(ctx, db, "migrations"); err != nil {
		_ = db.Close()
		return nil, fmt.Errorf("postgres: migrate: %w", err)
	}
	return &Store{db: db, signer: signer}, nil
}

// Close closes the pool.
func (s *Store) Close() error { return s.db.Close() }

// DB exposes the handle for server-side tables outside the ledger.
func (s *Store) DB() *sql.DB { return s.db }

// EnsureOrg creates the tenant if needed.
func (s *Store) EnsureOrg(ctx context.Context, org string) error {
	if org == "" {
		return errors.New("postgres: org id required")
	}
	_, err := s.db.ExecContext(ctx, `INSERT INTO org (id) VALUES ($1) ON CONFLICT DO NOTHING`, org)
	if err != nil {
		return err
	}
	_, err = s.db.ExecContext(ctx, `INSERT INTO org_head (org_id) VALUES ($1) ON CONFLICT DO NOTHING`, org)
	return err
}

// tx opens a transaction scoped to org (sets app.org_id for RLS).
func (s *Store) tx(ctx context.Context, org string) (*sql.Tx, error) {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return nil, err
	}
	if _, err := tx.ExecContext(ctx, `SELECT set_config('app.org_id', $1, true)`, org); err != nil {
		_ = tx.Rollback()
		return nil, err
	}
	return tx, nil
}
