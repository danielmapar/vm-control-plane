package store_test

import (
	"testing"

	"github.com/sigtunnel/vm-control-plane/internal/store"
	"github.com/sigtunnel/vm-control-plane/internal/store/pgtest"
)

// newStore returns a Store backed by a fresh per-test database. The package's
// single TestMain (vm_test.go) owns the embedded PostgreSQL instance.
func newStore(t *testing.T) *store.Store {
	t.Helper()
	return store.New(pgtest.NewDB(t))
}
