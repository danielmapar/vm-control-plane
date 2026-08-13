package store_test

import (
	"context"
	"crypto/sha256"
	"errors"
	"sync"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/sigtunnel/vm-control-plane/internal/store"
	"github.com/sigtunnel/vm-control-plane/internal/store/pgtest"
)

func hashOf(s string) []byte { h := sha256.Sum256([]byte(s)); return h[:] }

// claimCreate simulates the API's create path: claim envelope → (if owner)
// create operation + complete envelope, all in one transaction.
func claimCreate(ctx context.Context, s *store.Store, pool *pgxpool.Pool, key uuid.UUID, hash []byte) (*store.Operation, error) {
	tx, err := pool.Begin(ctx)
	if err != nil {
		return nil, err
	}
	defer tx.Rollback(ctx) //nolint:errcheck // no-op after commit

	existing, err := s.ClaimEnvelope(ctx, tx, key, "CreateVm", "v1", "vm", "web-1", hash)
	if err != nil {
		return nil, err
	}
	if existing != nil {
		return existing, tx.Commit(ctx)
	}

	op := store.CreateOperationParams{
		ID: uuid.New(), ResourceType: "vm", ResourceID: uuid.New(),
		ResourceName: "web-1", Verb: store.VerbCreate, TargetRevision: 1,
	}
	created, err := s.CreateOperation(ctx, tx, op)
	if err != nil {
		return nil, err
	}
	if err := s.CompleteEnvelope(ctx, tx, key, created.ID); err != nil {
		return nil, err
	}
	return created, tx.Commit(ctx)
}

func TestEnvelopeReplayReturnsOriginal(t *testing.T) {
	pool := pgtest.NewDB(t)
	s := store.New(pool)
	ctx := context.Background()
	key := uuid.New()
	hash := hashOf("req-a")

	first, err := claimCreate(ctx, s, pool, key, hash)
	if err != nil {
		t.Fatalf("first: %v", err)
	}
	second, err := claimCreate(ctx, s, pool, key, hash)
	if err != nil {
		t.Fatalf("replay: %v", err)
	}
	if first.ID != second.ID {
		t.Fatalf("replay produced a different operation: %s vs %s", first.ID, second.ID)
	}
}

func TestEnvelopeMismatchRejected(t *testing.T) {
	pool := pgtest.NewDB(t)
	s := store.New(pool)
	ctx := context.Background()
	key := uuid.New()

	if _, err := claimCreate(ctx, s, pool, key, hashOf("req-a")); err != nil {
		t.Fatal(err)
	}
	_, err := claimCreate(ctx, s, pool, key, hashOf("req-DIFFERENT"))
	if !errors.Is(err, store.ErrEnvelopeMismatch) {
		t.Fatalf("want ErrEnvelopeMismatch, got %v", err)
	}
}

// TestEnvelopeConcurrentSameKey: N racers, one key — exactly one operation
// exists afterward and every racer that succeeded saw that same operation.
func TestEnvelopeConcurrentSameKey(t *testing.T) {
	pool := pgtest.NewDB(t)
	s := store.New(pool)
	ctx := context.Background()
	key := uuid.New()
	hash := hashOf("req-a")

	const n = 8
	var wg sync.WaitGroup
	ids := make([]uuid.UUID, n)
	errs := make([]error, n)
	for i := 0; i < n; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			// Retry ErrEnvelopeIncomplete: under READ COMMITTED a loser can
			// hit the conflict before the winner's row is visible.
			for {
				op, err := claimCreate(ctx, s, pool, key, hash)
				if errors.Is(err, store.ErrEnvelopeIncomplete) {
					time.Sleep(10 * time.Millisecond)
					continue
				}
				if err == nil {
					ids[i] = op.ID
				}
				errs[i] = err
				return
			}
		}(i)
	}
	wg.Wait()

	var want uuid.UUID
	for i := 0; i < n; i++ {
		if errs[i] != nil {
			t.Fatalf("racer %d: %v", i, errs[i])
		}
		if want == uuid.Nil {
			want = ids[i]
		}
		if ids[i] != want {
			t.Fatalf("racer %d saw operation %s, want %s", i, ids[i], want)
		}
	}
}

// TestEnvelopeWinnerRollback: an envelope whose transaction rolled back
// leaves nothing behind — the next claimant becomes the owner.
func TestEnvelopeWinnerRollback(t *testing.T) {
	pool := pgtest.NewDB(t)
	s := store.New(pool)
	ctx := context.Background()
	key := uuid.New()
	hash := hashOf("req-a")

	tx, err := pool.Begin(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := s.ClaimEnvelope(ctx, tx, key, "CreateVm", "v1", "vm", "web-1", hash); err != nil {
		t.Fatal(err)
	}
	if err := tx.Rollback(ctx); err != nil {
		t.Fatal(err)
	}

	op, err := claimCreate(ctx, s, pool, key, hash)
	if err != nil || op == nil {
		t.Fatalf("post-rollback claim should own the verb: %v", err)
	}
}

func TestTerminalResultsImmutable(t *testing.T) {
	pool := pgtest.NewDB(t)
	s := store.New(pool)
	ctx := context.Background()

	op, err := s.CreateOperation(ctx, nil, store.CreateOperationParams{
		ID: uuid.New(), ResourceType: "vm", ResourceID: uuid.New(),
		ResourceName: "x", Verb: store.VerbCreate, TargetRevision: 1,
	})
	if err != nil {
		t.Fatal(err)
	}

	done, err := s.TerminalizeOperation(ctx, nil, op.ID, store.OpDone, "")
	if err != nil || done.State != store.OpDone {
		t.Fatalf("terminalize: %v %+v", err, done)
	}

	// A later, conflicting terminalization must NOT rewrite the result.
	again, err := s.TerminalizeOperation(ctx, nil, op.ID, store.OpFailed, "late loser")
	if err != nil {
		t.Fatal(err)
	}
	if again.State != store.OpDone || again.Error != "" {
		t.Fatalf("terminal result was rewritten: %+v", again)
	}
}

// TestDeadlineExpiryTerminalizes — an operation with no
// retry activity still terminates once its database-clock deadline passes.
func TestDeadlineExpiryTerminalizes(t *testing.T) {
	pool := pgtest.NewDB(t)
	s := store.New(pool)
	ctx := context.Background()

	op, err := s.CreateOperation(ctx, nil, store.CreateOperationParams{
		ID: uuid.New(), ResourceType: "vm", ResourceID: uuid.New(),
		ResourceName: "stall", Verb: store.VerbCreate, TargetRevision: 1,
		DeadlineBudget: -time.Second, // database-clock deadline already past
	})
	if err != nil {
		t.Fatal(err)
	}

	expired, err := s.ExpireOperations(ctx, nil)
	if err != nil {
		t.Fatal(err)
	}
	if len(expired) != 1 || expired[0].ID != op.ID || expired[0].State != store.OpDeadlineExceeded {
		t.Fatalf("expiry: %+v", expired)
	}

	// Idempotent: a second sweep finds nothing.
	expired, err = s.ExpireOperations(ctx, nil)
	if err != nil || len(expired) != 0 {
		t.Fatalf("second sweep: %v %+v", err, expired)
	}
}

// TestDeleteOperationOutlivesResource: operations are never FK-cascaded.
func TestDeleteOperationOutlivesResource(t *testing.T) {
	pool := pgtest.NewDB(t)
	s := store.New(pool)
	ctx := context.Background()

	vm, err := s.CreateVM(ctx, nil, uuid.New(), "gone-1", spec())
	if err != nil {
		t.Fatal(err)
	}
	op, err := s.CreateOperation(ctx, nil, store.CreateOperationParams{
		ID: uuid.New(), ResourceType: "vm", ResourceID: vm.ID,
		ResourceName: vm.Name, Verb: store.VerbDelete, TargetRevision: 2,
	})
	if err != nil {
		t.Fatal(err)
	}

	// Hard-delete the resource row (finalization does this after teardown).
	if _, err := pool.Exec(ctx, `DELETE FROM vms WHERE id=$1`, vm.ID); err != nil {
		t.Fatal(err)
	}

	got, err := s.GetOperation(ctx, nil, op.ID)
	if err != nil || got.ResourceName != "gone-1" {
		t.Fatalf("delete operation must outlive its resource: %v %+v", err, got)
	}
}
