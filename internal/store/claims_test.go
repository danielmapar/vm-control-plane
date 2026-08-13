package store_test

import (
	"context"
	"errors"
	"sync"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/sigtunnel/vm-control-plane/internal/store"
)

// TestClaimExclusive: two workers scanning concurrently never claim the
// same row.
func TestClaimExclusive(t *testing.T) {
	s := newStore(t)
	ctx := context.Background()

	for _, n := range []string{"cl-a", "cl-b", "cl-c", "cl-d"} {
		if _, err := s.CreateVM(ctx, nil, uuid.New(), n, spec()); err != nil {
			t.Fatal(err)
		}
	}

	var wg sync.WaitGroup
	got := make([][]*store.Claim, 2)
	for i := 0; i < 2; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			claims, err := s.ClaimDirtyVMs(ctx, "worker-"+string(rune('a'+i)), time.Minute, 10)
			if err != nil {
				t.Error(err)
				return
			}
			got[i] = claims
		}(i)
	}
	wg.Wait()

	seen := map[uuid.UUID]string{}
	for i, claims := range got {
		for _, c := range claims {
			if owner, dup := seen[c.VM.ID]; dup {
				t.Fatalf("vm %s claimed by both %s and worker-%d", c.VM.Name, owner, i)
			}
			seen[c.VM.ID] = c.Owner
		}
	}
	if len(seen) != 4 {
		t.Fatalf("expected all 4 rows claimed exactly once, got %d", len(seen))
	}
}

// TestClaimedRowInvisible: a live claim hides the row from other scans.
func TestClaimedRowInvisible(t *testing.T) {
	s := newStore(t)
	ctx := context.Background()
	if _, err := s.CreateVM(ctx, nil, uuid.New(), "cl-hidden", spec()); err != nil {
		t.Fatal(err)
	}

	first, err := s.ClaimDirtyVMs(ctx, "w1", time.Minute, 10)
	if err != nil || len(first) != 1 {
		t.Fatalf("first claim: %v (%d)", err, len(first))
	}
	second, err := s.ClaimDirtyVMs(ctx, "w2", time.Minute, 10)
	if err != nil {
		t.Fatal(err)
	}
	if len(second) != 0 {
		t.Fatalf("claimed row must be invisible while leased, got %d", len(second))
	}
}

// TestExpiredClaimLoserLoses: worker A's lease expires; worker B reclaims; A's
// guarded completion must fail with ErrClaimLost and roll back everything.
func TestExpiredClaimLoserLoses(t *testing.T) {
	s := newStore(t)
	ctx := context.Background()
	if _, err := s.CreateVM(ctx, nil, uuid.New(), "cl-exp", spec()); err != nil {
		t.Fatal(err)
	}

	a, err := s.ClaimDirtyVMs(ctx, "worker-a", 50*time.Millisecond, 1)
	if err != nil || len(a) != 1 {
		t.Fatalf("claim A: %v", err)
	}
	time.Sleep(120 * time.Millisecond) // lease expires (database clock)

	b, err := s.ClaimDirtyVMs(ctx, "worker-b", time.Minute, 1)
	if err != nil || len(b) != 1 {
		t.Fatalf("claim B after expiry: %v (%d)", err, len(b))
	}

	// A limps back and tries to commit its transition. CompleteClaimTx may
	// even succeed in taking the row lock, but FinishClaim's lease guard is
	// the real gate — the release is the LAST statement.
	atx, err := s.CompleteClaimTx(ctx, a[0].VM.ID, a[0].Token)
	if err == nil {
		ferr := s.FinishClaim(ctx, atx, a[0].VM.ID, a[0].Token, 0)
		_ = atx.Rollback(ctx)
		if !errors.Is(ferr, store.ErrClaimLost) {
			t.Fatalf("stale worker must lose at FinishClaim, got %v", ferr)
		}
	} else if !errors.Is(err, store.ErrClaimLost) {
		t.Fatalf("stale worker: want ErrClaimLost, got %v", err)
	}

	// B's completion succeeds through the full protocol.
	tx, err := s.CompleteClaimTx(ctx, b[0].VM.ID, b[0].Token)
	if err != nil {
		t.Fatalf("B complete: %v", err)
	}
	if err := s.FinishClaim(ctx, tx, b[0].VM.ID, b[0].Token, time.Hour); err != nil {
		t.Fatalf("B finish: %v", err)
	}
	if err := tx.Commit(ctx); err != nil {
		t.Fatal(err)
	}
}

// TestExpiryWithoutTakeover: even with NO takeover, an expired lease alone
// blocks the commit (a token must not outlive its lease).
func TestExpiryWithoutTakeover(t *testing.T) {
	s := newStore(t)
	ctx := context.Background()
	if _, err := s.CreateVM(ctx, nil, uuid.New(), "cl-exp2", spec()); err != nil {
		t.Fatal(err)
	}
	a, err := s.ClaimDirtyVMs(ctx, "worker-a", 50*time.Millisecond, 1)
	if err != nil || len(a) != 1 {
		t.Fatal(err)
	}
	time.Sleep(120 * time.Millisecond)

	atx, err := s.CompleteClaimTx(ctx, a[0].VM.ID, a[0].Token)
	if err == nil {
		ferr := s.FinishClaim(ctx, atx, a[0].VM.ID, a[0].Token, 0)
		_ = atx.Rollback(ctx)
		if !errors.Is(ferr, store.ErrClaimLost) {
			t.Fatalf("expired-without-takeover must lose at FinishClaim, got %v", ferr)
		}
	} else if !errors.Is(err, store.ErrClaimLost) {
		t.Fatalf("expired-without-takeover: got %v", err)
	}
}

// TestRenewSemantics: renewal under the token keeps the claim alive; renewal
// after expiry fails.
func TestRenewSemantics(t *testing.T) {
	s := newStore(t)
	ctx := context.Background()
	if _, err := s.CreateVM(ctx, nil, uuid.New(), "cl-renew", spec()); err != nil {
		t.Fatal(err)
	}
	a, err := s.ClaimDirtyVMs(ctx, "w", 300*time.Millisecond, 1)
	if err != nil || len(a) != 1 {
		t.Fatal(err)
	}
	for i := 0; i < 3; i++ {
		time.Sleep(100 * time.Millisecond)
		if err := s.RenewClaim(ctx, a[0].VM.ID, a[0].Token, 300*time.Millisecond); err != nil {
			t.Fatalf("renew %d: %v", i, err)
		}
	}
	// Now let it lapse and confirm renewal fails.
	time.Sleep(400 * time.Millisecond)
	if err := s.RenewClaim(ctx, a[0].VM.ID, a[0].Token, time.Minute); !errors.Is(err, store.ErrClaimLost) {
		t.Fatalf("renew after expiry: want ErrClaimLost, got %v", err)
	}
}

// TestFailureBudgetParksFailedAndTerminalizes: durable retry state and the
// guarantee that op wait cannot hang on a Failed resource.
func TestFailureBudgetParksFailedAndTerminalizes(t *testing.T) {
	s := newStore(t)
	ctx := context.Background()
	vm, err := s.CreateVM(ctx, nil, uuid.New(), "cl-fail", spec())
	if err != nil {
		t.Fatal(err)
	}
	op, err := s.CreateOperation(ctx, nil, store.CreateOperationParams{
		ID: uuid.New(), ResourceType: "vm", ResourceID: vm.ID, ResourceName: vm.Name,
		Verb: store.VerbCreate, TargetRevision: 1,
	})
	if err != nil {
		t.Fatal(err)
	}

	const budget = 3
	for i := 1; i <= budget; i++ {
		claims, err := s.ClaimDirtyVMs(ctx, "w", time.Minute, 1)
		if err != nil || len(claims) != 1 {
			t.Fatalf("attempt %d claim: %v (%d)", i, err, len(claims))
		}
		failed, err := s.RecordFailure(ctx, claims[0].VM.ID, claims[0].Token,
			"transient", "driver exploded", 0, budget, &op.ID)
		if err != nil {
			t.Fatalf("attempt %d record: %v", i, err)
		}
		if (i == budget) != failed {
			t.Fatalf("attempt %d: failed=%v, want %v", i, failed, i == budget)
		}
	}

	// FAILED rows leave the queue…
	claims, err := s.ClaimDirtyVMs(ctx, "w", time.Minute, 10)
	if err != nil || len(claims) != 0 {
		t.Fatalf("FAILED row must not be claimable: %v (%d)", err, len(claims))
	}
	// …and the operation is terminal (op wait cannot hang).
	final, err := s.GetOperation(ctx, nil, op.ID)
	if err != nil || final.State != store.OpFailed || !final.Terminal() {
		t.Fatalf("operation not terminalized with the phase write: %v %+v", err, final)
	}
}

// TestCompletionUnderObservationTraffic: resource_version churn from other
// writers must not invalidate a valid completion. The guard is the claim
// token + lease, not the global version.
func TestCompletionUnderObservationTraffic(t *testing.T) {
	s := newStore(t)
	ctx := context.Background()
	vm, err := s.CreateVM(ctx, nil, uuid.New(), "cl-obs", spec())
	if err != nil {
		t.Fatal(err)
	}
	claims, err := s.ClaimDirtyVMs(ctx, "w", time.Minute, 1)
	if err != nil || len(claims) != 1 {
		t.Fatal(err)
	}

	// Simulated observation traffic: bump resource_version repeatedly.
	for i := 0; i < 5; i++ {
		if _, err := s.Pool().Exec(ctx,
			`UPDATE vms SET resource_version = resource_version + 1 WHERE id=$1`, vm.ID); err != nil {
			t.Fatal(err)
		}
	}

	tx, err := s.CompleteClaimTx(ctx, claims[0].VM.ID, claims[0].Token)
	if err != nil {
		t.Fatalf("completion must survive observation traffic: %v", err)
	}
	if err := s.FinishClaim(ctx, tx, claims[0].VM.ID, claims[0].Token, time.Hour); err != nil {
		t.Fatalf("finish must survive observation traffic: %v", err)
	}
	if err := tx.Commit(ctx); err != nil {
		t.Fatal(err)
	}
}
