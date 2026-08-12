package store_test

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/sigtunnel/vm-control-plane/internal/store"
)

// reportFixture: registered host, placed + granted VM — ready for evidence.
func reportFixture(t *testing.T) (*store.Store, store.Session, *store.VM, int64) {
	s, sess, vm, epoch := grantFixture(t)
	if err := s.GrantExecution(context.Background(), sess, vm.ID, epoch); err != nil {
		t.Fatal(err)
	}
	return s, sess, vm, epoch
}

//nolint:unparam // rev varies as evidence scenarios grow
func mkReport(sess store.Session, vm *store.VM, epoch, seq, rev int64, state string) store.Report {
	return store.Report{Session: sess, VMID: vm.ID, Epoch: epoch, Seq: seq, AppliedRevision: rev, State: state}
}

// TestReportOrdering: matrix rows 17 — (session_generation, report_seq)
// lexicographic. A delayed RUNNING cannot overwrite newer SHUTOFF drift
// evidence; a fresh session's seq 1 beats the old session's seq 99.
func TestReportOrdering(t *testing.T) {
	s, sess, vm, epoch := reportFixture(t)
	ctx := context.Background()

	if err := s.ApplyReport(ctx, mkReport(sess, vm, epoch, 1, 1, "RUNNING")); err != nil {
		t.Fatalf("seq1: %v", err)
	}
	if err := s.ApplyReport(ctx, mkReport(sess, vm, epoch, 3, 1, "SHUTOFF")); err != nil {
		t.Fatalf("seq3: %v", err)
	}
	// Delayed seq2 (same session): rejected — SHUTOFF evidence stands.
	err := s.ApplyReport(ctx, mkReport(sess, vm, epoch, 2, 1, "RUNNING"))
	if !errors.Is(err, store.ErrStaleReport) {
		t.Fatalf("delayed same-session report: want ErrStaleReport, got %v", err)
	}

	got, err := s.GetVM(ctx, nil, vm.Name)
	if err != nil || got.Status == nil {
		t.Fatal(err)
	}

	// Replacement daemon: fresh session generation, seq restarts at 1.
	sessions, err := s.RegisterHost(ctx, host("h1"), twoNodes(), time.Minute)
	if err != nil {
		t.Fatal(err)
	}
	var newSess store.Session
	for _, x := range sessions {
		if x.NodeName == "node-a" {
			newSess = x
		}
	}
	if err := s.ApplyReport(ctx, mkReport(newSess, vm, epoch, 1, 1, "RUNNING")); err != nil {
		t.Fatalf("new-session seq1 must beat old-session seq3 lexicographically: %v", err)
	}
	// Delayed old-session seq99: rejected (stale generation).
	err = s.ApplyReport(ctx, mkReport(sess, vm, epoch, 99, 1, "SHUTOFF"))
	if !errors.Is(err, store.ErrStaleReport) {
		t.Fatalf("old-session high seq: want ErrStaleReport, got %v", err)
	}
}

// TestReportEpochFence: evidence for a superseded epoch is rejected.
func TestReportEpochFence(t *testing.T) {
	s, sess, vm, epoch := reportFixture(t)
	ctx := context.Background()

	err := s.ApplyReport(ctx, mkReport(sess, vm, epoch+7, 1, 1, "RUNNING"))
	if !errors.Is(err, store.ErrStaleReport) {
		t.Fatalf("wrong-epoch report: want ErrStaleReport, got %v", err)
	}
}

// TestActionPacingDurable: the server owns retry pacing; a duplicate
// failure report with a stale token is a no-op (idempotent acceptance).
func TestActionPacingDurable(t *testing.T) {
	s, _, vm, epoch := reportFixture(t)
	ctx := context.Background()

	ms, token, err := s.NextActionAttempt(ctx, vm.ID, epoch, 1)
	if err != nil || ms != 0 {
		t.Fatalf("first attempt should be due now: %d %v", ms, err)
	}
	// Same call returns the same token (no accidental re-mint).
	_, token2, err := s.NextActionAttempt(ctx, vm.ID, epoch, 1)
	if err != nil || token2 != token {
		t.Fatalf("token stability: %v %s vs %s", err, token, token2)
	}

	if err := s.RecordActionFailure(ctx, vm.ID, epoch, 1, token, 60_000); err != nil {
		t.Fatal(err)
	}
	ms, token3, err := s.NextActionAttempt(ctx, vm.ID, epoch, 1)
	if err != nil || ms <= 0 {
		t.Fatalf("failure must push not_before: %d %v", ms, err)
	}
	if token3 == token {
		t.Fatal("failure must mint a fresh attempt token")
	}

	// Duplicate/late failure with the OLD token: no-op.
	if err := s.RecordActionFailure(ctx, vm.ID, epoch, 1, token, 600_000); err != nil {
		t.Fatal(err)
	}
	var attempts int
	if err := s.Pool().QueryRow(ctx,
		`SELECT attempts FROM action_retries WHERE vm_id=$1 AND epoch=$2 AND revision=$3`,
		vm.ID, epoch, int64(1)).Scan(&attempts); err != nil {
		t.Fatal(err)
	}
	if attempts != 1 {
		t.Fatalf("stale-token failure must not double-count: attempts=%d", attempts)
	}
}

// TestTeardownReceiptIdempotent: receipts release capacity exactly once
// and survive replay (matrix row 16 foundation).
func TestTeardownReceiptIdempotent(t *testing.T) {
	s, _, vm, epoch := reportFixture(t)
	ctx := context.Background()

	for i := 0; i < 2; i++ {
		if err := s.MarkTeardownComplete(ctx, vm.ID, epoch); err != nil {
			t.Fatalf("receipt %d: %v", i, err)
		}
	}
	n, err := s.GetNode(ctx, "node-a")
	if err != nil || n.ReservedCPUs != 0 {
		t.Fatalf("capacity after receipts: %+v", n)
	}
	p, err := s.GetPlacement(ctx, nil, vm.ID, epoch)
	if err != nil || p.State != "torn_down" {
		t.Fatalf("ledger after receipts: %+v", p)
	}
}
