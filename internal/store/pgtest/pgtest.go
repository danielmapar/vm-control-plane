// Package pgtest runs one embedded PostgreSQL per test package and hands
// each test its own database. The configuration is deliberate, not default
// (plan D11, all verified on the Windows dev machine):
//
//   - pinned PostgreSQL major version, matching CI — no floating majors;
//   - UTF-8 + locale C: Windows initdb otherwise defaults to WIN1252 and
//     collation-sensitive behavior silently diverges from Linux;
//   - per-instance data/runtime dirs under the OS temp dir and a port from
//     net.Listen(":0") — the library's shared defaults collide across
//     parallel test packages;
//   - first-run binary extraction serialized behind a file lock — parallel
//     cold starts race on the shared cache directory;
//   - Stop() before cleanup, and cleanup retries — Windows keeps file locks
//     briefly after postgres.exe exits.
//
// Usage:
//
//	func TestMain(m *testing.M) { os.Exit(pgtest.Main(m)) }
//	func TestX(t *testing.T)    { pool := pgtest.NewDB(t) … }
package pgtest

import (
	"context"
	"fmt"
	"net"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	embeddedpostgres "github.com/fergusstrange/embedded-postgres"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/sigtunnel/vm-control-plane/internal/store"
)

// Version is the pinned PostgreSQL major used everywhere (tests and CI).
const Version = embeddedpostgres.V16

var (
	pg       *embeddedpostgres.EmbeddedPostgres
	basePort int
	dbSeq    atomic.Int64
)

// connURL builds a connection URL for the given database on the
// package-scoped instance. Built structurally — naive string replacement on
// a URL whose username is also "postgres" corrupts the userinfo.
func connURL(db string) string {
	return fmt.Sprintf("postgres://postgres:postgres@127.0.0.1:%d/%s", basePort, db)
}

// Main starts the package-scoped instance, runs the tests, and tears down.
func Main(m *testing.M) int {
	port, err := freePort()
	if err != nil {
		fmt.Fprintln(os.Stderr, "pgtest: no free port:", err)
		return 1
	}
	dir, err := os.MkdirTemp("", "vmc-pgtest-*")
	if err != nil {
		fmt.Fprintln(os.Stderr, "pgtest: temp dir:", err)
		return 1
	}

	unlock, err := extractionLock()
	if err != nil {
		fmt.Fprintln(os.Stderr, "pgtest: extraction lock:", err)
		return 1
	}

	pg = embeddedpostgres.NewDatabase(embeddedpostgres.DefaultConfig().
		Version(Version).
		Encoding("UTF8").
		Locale("C").
		Port(uint32(port)).
		DataPath(filepath.Join(dir, "data")).
		RuntimePath(filepath.Join(dir, "runtime")).
		Logger(nil))

	if err := pg.Start(); err != nil {
		unlock()
		fmt.Fprintln(os.Stderr, "pgtest: start:", err)
		if strings.Contains(err.Error(), "process could not be started") {
			fmt.Fprintln(os.Stderr, "pgtest: hint: postgres refuses elevated shells — run tests from a non-admin terminal")
		}
		return 1
	}
	unlock() // binaries are extracted once Start returns

	basePort = port

	code := m.Run()

	if err := pg.Stop(); err != nil {
		fmt.Fprintln(os.Stderr, "pgtest: stop:", err)
	}
	// Windows: postgres.exe may hold locks briefly after Stop returns.
	for i := 0; i < 10; i++ {
		if err := os.RemoveAll(dir); err == nil {
			break
		}
		time.Sleep(200 * time.Millisecond)
	}
	return code
}

// NewDB creates a fresh database for the test, applies migrations, and
// returns a pool that closes with the test.
func NewDB(t *testing.T) *pgxpool.Pool {
	t.Helper()
	if basePort == 0 {
		t.Fatal("pgtest.NewDB called without pgtest.Main in TestMain")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	name := fmt.Sprintf("vmc_test_%d_%d", os.Getpid(), dbSeq.Add(1))

	admin, err := pgxpool.New(ctx, connURL("postgres"))
	if err != nil {
		t.Fatalf("pgtest: admin pool: %v", err)
	}
	defer admin.Close()
	if _, err := admin.Exec(ctx, "CREATE DATABASE "+name); err != nil {
		t.Fatalf("pgtest: create database: %v", err)
	}

	pool, err := pgxpool.New(ctx, connURL(name))
	if err != nil {
		t.Fatalf("pgtest: pool: %v", err)
	}
	if err := store.Migrate(ctx, pool); err != nil {
		t.Fatalf("pgtest: migrate: %v", err)
	}
	t.Cleanup(pool.Close)
	return pool
}

func freePort() (int, error) {
	l, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		return 0, err
	}
	defer l.Close() //nolint:errcheck // probe listener
	return l.Addr().(*net.TCPAddr).Port, nil
}

// extractionLock serializes first-run binary extraction across concurrent
// test packages sharing the download cache.
func extractionLock() (func(), error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return nil, err
	}
	cache := filepath.Join(home, ".embedded-postgres-go")
	if err := os.MkdirAll(cache, 0o755); err != nil {
		return nil, err
	}
	lockPath := filepath.Join(cache, ".vmc-extract-lock")
	deadline := time.Now().Add(5 * time.Minute)
	for {
		f, err := os.OpenFile(lockPath, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0o644)
		if err == nil {
			return func() {
				_ = f.Close()
				_ = os.Remove(lockPath)
			}, nil
		}
		if time.Now().After(deadline) {
			// A crashed holder can strand the lock; steal it after the wait.
			_ = os.Remove(lockPath)
		}
		time.Sleep(250 * time.Millisecond)
	}
}
