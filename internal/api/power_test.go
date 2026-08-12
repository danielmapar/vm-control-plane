package api_test

import (
	"context"
	"testing"

	"github.com/google/uuid"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	vmcv1 "github.com/sigtunnel/vm-control-plane/proto/vmc/v1"
)

//nolint:unparam // both power states exercised as scenarios grow
func powerReq(name string, p vmcv1.PowerState) *vmcv1.UpdateVmPowerRequest {
	return &vmcv1.UpdateVmPowerRequest{
		IdempotencyKey: uuid.NewString(), Name: name, Power: p,
	}
}

// TestPowerUpdateBumpsRevisions: the only v0.1-mutable field moves both
// spec_generation and desired_revision exactly once per accepted update.
func TestPowerUpdateBumpsRevisions(t *testing.T) {
	s := newServer(t)
	ctx := context.Background()
	if _, err := s.CreateVm(ctx, createReq("pw-1")); err != nil {
		t.Fatal(err)
	}

	op, err := s.UpdateVmPower(ctx, powerReq("pw-1", vmcv1.PowerState_POWER_STATE_STOPPED))
	if err != nil {
		t.Fatalf("power update: %v", err)
	}
	if op.GetVerb() != vmcv1.Verb_VERB_UPDATE_POWER || op.GetTargetRevision() != 2 {
		t.Fatalf("operation identity: %+v", op)
	}

	vm, err := s.GetVm(ctx, &vmcv1.GetVmRequest{Name: "pw-1"})
	if err != nil {
		t.Fatal(err)
	}
	if vm.GetMeta().GetSpecGeneration() != 2 || vm.GetMeta().GetDesiredRevision() != 2 {
		t.Fatalf("revisions after power update: %+v", vm.GetMeta())
	}
	if vm.GetSpec().GetPower() != vmcv1.PowerState_POWER_STATE_STOPPED {
		t.Fatalf("power not persisted: %v", vm.GetSpec().GetPower())
	}
}

// TestPowerReplayIdempotent: same key + same request → same operation, one
// revision bump total.
func TestPowerReplayIdempotent(t *testing.T) {
	s := newServer(t)
	ctx := context.Background()
	if _, err := s.CreateVm(ctx, createReq("pw-2")); err != nil {
		t.Fatal(err)
	}

	req := powerReq("pw-2", vmcv1.PowerState_POWER_STATE_STOPPED)
	op1, err := s.UpdateVmPower(ctx, req)
	if err != nil {
		t.Fatal(err)
	}
	op2, err := s.UpdateVmPower(ctx, req)
	if err != nil || op2.GetId() != op1.GetId() {
		t.Fatalf("replay: %v (%s vs %s)", err, op1.GetId(), op2.GetId())
	}
	vm, err := s.GetVm(ctx, &vmcv1.GetVmRequest{Name: "pw-2"})
	if err != nil || vm.GetMeta().GetDesiredRevision() != 2 {
		t.Fatalf("replay must not double-bump: rev=%d", vm.GetMeta().GetDesiredRevision())
	}
}

// TestPowerPreconditionRejected: a stale user-supplied resource_version is
// FAILED_PRECONDITION-shaped (mapped from Aborted) and NOT retried.
func TestPowerPreconditionRejected(t *testing.T) {
	s := newServer(t)
	ctx := context.Background()
	if _, err := s.CreateVm(ctx, createReq("pw-3")); err != nil {
		t.Fatal(err)
	}
	vm, err := s.GetVm(ctx, &vmcv1.GetVmRequest{Name: "pw-3"})
	if err != nil {
		t.Fatal(err)
	}

	req := powerReq("pw-3", vmcv1.PowerState_POWER_STATE_STOPPED)
	req.ExpectedResourceVersion = vm.GetMeta().GetResourceVersion() + 99
	_, err = s.UpdateVmPower(ctx, req)
	if status.Code(err) != codes.Aborted {
		t.Fatalf("stale precondition: want Aborted, got %v", err)
	}
}

// TestPowerOnDeletingVMRejected: tombstoned rows accept no spec updates.
func TestPowerOnDeletingVMRejected(t *testing.T) {
	s := newServer(t)
	ctx := context.Background()
	if _, err := s.CreateVm(ctx, createReq("pw-4")); err != nil {
		t.Fatal(err)
	}
	if _, err := s.DeleteVm(ctx, &vmcv1.DeleteVmRequest{IdempotencyKey: uuid.NewString(), Name: "pw-4"}); err != nil {
		t.Fatal(err)
	}
	_, err := s.UpdateVmPower(ctx, powerReq("pw-4", vmcv1.PowerState_POWER_STATE_STOPPED))
	if status.Code(err) != codes.FailedPrecondition {
		t.Fatalf("power on Deleting vm: want FailedPrecondition, got %v", err)
	}
}
