package store

import (
	"context"
	"embed"
	"fmt"
	"sort"

	"github.com/jackc/pgx/v5/pgxpool"
)

//go:embed migrations/*.sql
var migrationFS embed.FS

// Migrate applies embedded migrations in filename order, tracking progress
// in schema_migrations. PostgreSQL DDL is transactional, so each migration
// applies atomically: a crash mid-migration leaves it unapplied, not half
// applied.
func Migrate(ctx context.Context, pool *pgxpool.Pool) error {
	// One dedicated connection holds the advisory lock for the whole run:
	// concurrent processes (api + controller roles starting together)
	// serialize here instead of racing DDL (PR 4-8 triage).
	lockConn, err := pool.Acquire(ctx)
	if err != nil {
		return fmt.Errorf("migration lock conn: %w", err)
	}
	defer lockConn.Release()
	if _, err := lockConn.Exec(ctx, `SELECT pg_advisory_lock(727274)`); err != nil {
		return fmt.Errorf("migration lock: %w", err)
	}
	defer func() { _, _ = lockConn.Exec(context.WithoutCancel(ctx), `SELECT pg_advisory_unlock(727274)`) }()

	if _, err := pool.Exec(ctx, `CREATE TABLE IF NOT EXISTS schema_migrations (
		filename text PRIMARY KEY,
		applied_at timestamptz NOT NULL DEFAULT now()
	)`); err != nil {
		return fmt.Errorf("migrations table: %w", err)
	}

	entries, err := migrationFS.ReadDir("migrations")
	if err != nil {
		return fmt.Errorf("read migrations: %w", err)
	}
	names := make([]string, 0, len(entries))
	for _, e := range entries {
		names = append(names, e.Name())
	}
	sort.Strings(names)

	for _, name := range names {
		tx, err := pool.Begin(ctx)
		if err != nil {
			return err
		}
		var applied bool
		if err := tx.QueryRow(ctx,
			`SELECT EXISTS(SELECT 1 FROM schema_migrations WHERE filename=$1)`, name,
		).Scan(&applied); err != nil {
			_ = tx.Rollback(ctx)
			return err
		}
		if applied {
			_ = tx.Rollback(ctx)
			continue
		}
		sql, err := migrationFS.ReadFile("migrations/" + name)
		if err != nil {
			_ = tx.Rollback(ctx)
			return err
		}
		if _, err := tx.Exec(ctx, string(sql)); err != nil {
			_ = tx.Rollback(ctx)
			return fmt.Errorf("apply %s: %w", name, err)
		}
		if _, err := tx.Exec(ctx,
			`INSERT INTO schema_migrations (filename) VALUES ($1)`, name); err != nil {
			_ = tx.Rollback(ctx)
			return err
		}
		if err := tx.Commit(ctx); err != nil {
			return err
		}
	}
	return nil
}
