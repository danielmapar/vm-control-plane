// Package api implements the public gRPC services. The API owns spec and
// tombstones and NEVER writes phase or status — those belong to the
// reconciler; the two share only the store (ADR-0001/0002, plan §6).
package api

import (
	"context"
	"errors"
	"log/slog"
	"time"

	"github.com/google/uuid"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/types/known/timestamppb"

	"github.com/sigtunnel/vm-control-plane/internal/store"
	vmcv1 "github.com/sigtunnel/vm-control-plane/proto/vmc/v1"
)

// Verb deadlines (database-clock): the bound after which an operation
// terminalizes as DEADLINE_EXCEEDED even if nothing is retrying (D3).
const (
	createDeadline = 15 * time.Minute
	deleteDeadline = 30 * time.Minute
)

// envelope claim retry: under READ COMMITTED a concurrent winner's row may
// not be visible yet; we retry briefly, then surface retryable ABORTED.
const (
	envelopeRetries = 5
	envelopeBackoff = 50 * time.Millisecond
)

// Server implements vmc.v1.VMService and vmc.v1.OperationService.
type Server struct {
	vmcv1.UnimplementedVMServiceServer
	vmcv1.UnimplementedOperationServiceServer

	st  *store.Store
	log *slog.Logger
}

func NewServer(st *store.Store, log *slog.Logger) *Server {
	return &Server{st: st, log: log}
}

// CreateVm: envelope claim → insert VM + operation + complete envelope, one
// transaction. A replay returns the original operation; a reused key with a
// different request is FAILED_PRECONDITION.
func (s *Server) CreateVm(ctx context.Context, req *vmcv1.CreateVmRequest) (*vmcv1.Operation, error) {
	key, err := uuid.Parse(req.GetIdempotencyKey())
	if err != nil || key == uuid.Nil {
		return nil, status.Error(codes.InvalidArgument, "idempotency_key must be a non-nil UUID (minted client-side before the first attempt)")
	}
	// Normalize a CLONE (defaults applied without mutating the caller's
	// request), hash it, and claim the envelope BEFORE validation: a replay
	// must return its original operation even if validation policy changed
	// between attempts, and an altered request must hit the mismatch error,
	// not a validation error (PR 4-8 review triage).
	norm := proto.Clone(req).(*vmcv1.CreateVmRequest)
	norm.IdempotencyKey = ""
	normalizeSpec(norm.GetSpec())
	hash, err := canonicalHash(norm, func(proto.Message) {})
	if err != nil {
		return nil, status.Error(codes.Internal, err.Error())
	}
	req = &vmcv1.CreateVmRequest{IdempotencyKey: key.String(), Name: norm.GetName(), Spec: norm.GetSpec()}

	for attempt := 0; ; attempt++ {
		op, err := s.createVmTx(ctx, key, req, hash)
		switch {
		case err == nil:
			return op, nil
		case errors.Is(err, store.ErrEnvelopeIncomplete) && attempt < envelopeRetries:
			time.Sleep(envelopeBackoff)
		case errors.Is(err, store.ErrEnvelopeIncomplete):
			return nil, status.Error(codes.Aborted, "concurrent request with the same idempotency key is in flight; retry")
		default:
			return nil, mapStoreErr(err)
		}
	}
}

func (s *Server) createVmTx(ctx context.Context, key uuid.UUID, req *vmcv1.CreateVmRequest, hash []byte) (*vmcv1.Operation, error) {
	tx, err := s.st.Pool().Begin(ctx)
	if err != nil {
		return nil, err
	}
	defer tx.Rollback(ctx) //nolint:errcheck // no-op after commit

	existing, err := s.st.ClaimEnvelope(ctx, tx, key, "CreateVm", "v1", "vm", req.GetName(), hash)
	if err != nil {
		return nil, err
	}
	if existing != nil {
		if err := tx.Commit(ctx); err != nil {
			return nil, err
		}
		return operationToProto(existing), nil
	}

	// We own the verb: validate INSIDE the winning transaction (replays
	// never reach validation — they returned above).
	if err := validateName(req.GetName()); err != nil {
		return nil, status.Error(codes.InvalidArgument, err.Error())
	}
	if err := validateSpec(req.GetSpec()); err != nil {
		return nil, status.Error(codes.InvalidArgument, err.Error())
	}

	vm, err := s.st.CreateVM(ctx, tx, uuid.New(), req.GetName(), req.GetSpec())
	if err != nil {
		return nil, err
	}
	op, err := s.st.CreateOperation(ctx, tx, &store.Operation{
		ID: uuid.New(), ResourceType: "vm", ResourceID: vm.ID,
		ResourceName: vm.Name, Verb: "CREATE",
		TargetRevision: vm.DesiredRevision,
		DeadlineBudget: createDeadline,
	})
	if err != nil {
		return nil, err
	}
	if err := s.st.CompleteEnvelope(ctx, tx, key, op.ID); err != nil {
		return nil, err
	}
	if err := tx.Commit(ctx); err != nil {
		return nil, err
	}
	s.log.InfoContext(ctx, "vm create accepted",
		"vm", vm.Name, "operation", op.ID, "target_revision", op.TargetRevision)
	return operationToProto(op), nil
}

// UpdateVmPower: the only v0.1 spec mutation (D3). Envelope discipline
// identical to create: hash first, claim, then mutate inside the winning
// transaction with an optional resource_version precondition.
func (s *Server) UpdateVmPower(ctx context.Context, req *vmcv1.UpdateVmPowerRequest) (*vmcv1.Operation, error) {
	key, err := uuid.Parse(req.GetIdempotencyKey())
	if err != nil || key == uuid.Nil {
		return nil, status.Error(codes.InvalidArgument, "idempotency_key must be a non-nil UUID")
	}
	switch req.GetPower() {
	case vmcv1.PowerState_POWER_STATE_RUNNING, vmcv1.PowerState_POWER_STATE_STOPPED:
	default:
		return nil, status.Error(codes.InvalidArgument, "power must be RUNNING or STOPPED")
	}
	hash, err := canonicalHash(req, func(m proto.Message) {
		m.(*vmcv1.UpdateVmPowerRequest).IdempotencyKey = ""
	})
	if err != nil {
		return nil, status.Error(codes.Internal, err.Error())
	}

	for attempt := 0; ; attempt++ {
		op, err := s.updatePowerTx(ctx, key, req, hash)
		switch {
		case err == nil:
			return op, nil
		case errors.Is(err, store.ErrEnvelopeIncomplete) && attempt < envelopeRetries:
			time.Sleep(envelopeBackoff)
		case errors.Is(err, store.ErrEnvelopeIncomplete):
			return nil, status.Error(codes.Aborted, "concurrent request with the same idempotency key is in flight; retry")
		case errors.Is(err, store.ErrStaleWrite) && req.GetExpectedResourceVersion() == 0 && attempt < envelopeRetries:
			// No user precondition: transparent CAS retry is safe.
		default:
			return nil, mapStoreErr(err)
		}
	}
}

func (s *Server) updatePowerTx(ctx context.Context, key uuid.UUID, req *vmcv1.UpdateVmPowerRequest, hash []byte) (*vmcv1.Operation, error) {
	tx, err := s.st.Pool().Begin(ctx)
	if err != nil {
		return nil, err
	}
	defer tx.Rollback(ctx) //nolint:errcheck // no-op after commit

	existing, err := s.st.ClaimEnvelope(ctx, tx, key, "UpdateVmPower", "v1", "vm", req.GetName(), hash)
	if err != nil {
		return nil, err
	}
	if existing != nil {
		if err := tx.Commit(ctx); err != nil {
			return nil, err
		}
		return operationToProto(existing), nil
	}

	vm, err := s.st.GetVM(ctx, tx, req.GetName())
	if err != nil {
		return nil, err
	}
	if vm.DeletedAt != nil {
		return nil, status.Error(codes.FailedPrecondition, "vm is being deleted")
	}
	expect := vm.ResourceVersion
	if v := req.GetExpectedResourceVersion(); v != 0 {
		expect = v // user-supplied precondition wins — and is NOT retried
	}
	spec := proto.Clone(vm.Spec).(*vmcv1.VmSpec)
	spec.Power = req.GetPower()
	updated, err := s.st.UpdateVMPower(ctx, tx, vm.ID, expect, spec)
	if err != nil {
		return nil, err
	}
	op, err := s.st.CreateOperation(ctx, tx, &store.Operation{
		ID: uuid.New(), ResourceType: "vm", ResourceID: updated.ID,
		ResourceName: updated.Name, Verb: "UPDATE_POWER",
		TargetRevision: updated.DesiredRevision,
		DeadlineBudget: createDeadline,
	})
	if err != nil {
		return nil, err
	}
	if err := s.st.CompleteEnvelope(ctx, tx, key, op.ID); err != nil {
		return nil, err
	}
	if err := tx.Commit(ctx); err != nil {
		return nil, err
	}
	s.log.InfoContext(ctx, "vm power update accepted",
		"vm", updated.Name, "power", req.GetPower().String(), "operation", op.ID, "target_revision", op.TargetRevision)
	return operationToProto(op), nil
}

// DeleteVm: envelope claim → tombstone (set-once) + operation, one
// transaction. The row persists until teardown finalizes — deletion is a
// desired state, not an absence.
func (s *Server) DeleteVm(ctx context.Context, req *vmcv1.DeleteVmRequest) (*vmcv1.Operation, error) {
	key, err := uuid.Parse(req.GetIdempotencyKey())
	if err != nil || key == uuid.Nil {
		return nil, status.Error(codes.InvalidArgument, "idempotency_key must be a non-nil UUID")
	}
	hash, err := canonicalHash(req, func(m proto.Message) {
		m.(*vmcv1.DeleteVmRequest).IdempotencyKey = ""
	})
	if err != nil {
		return nil, status.Error(codes.Internal, err.Error())
	}

	for attempt := 0; ; attempt++ {
		op, err := s.deleteVmTx(ctx, key, req, hash)
		switch {
		case err == nil:
			return op, nil
		case errors.Is(err, store.ErrEnvelopeIncomplete) && attempt < envelopeRetries:
			time.Sleep(envelopeBackoff)
		case errors.Is(err, store.ErrEnvelopeIncomplete):
			return nil, status.Error(codes.Aborted, "concurrent request with the same idempotency key is in flight; retry")
		case errors.Is(err, store.ErrStaleWrite) && attempt < envelopeRetries:
			// Someone bumped resource_version between our read and CAS;
			// level-triggered re-read makes the retry safe.
		default:
			return nil, mapStoreErr(err)
		}
	}
}

func (s *Server) deleteVmTx(ctx context.Context, key uuid.UUID, req *vmcv1.DeleteVmRequest, hash []byte) (*vmcv1.Operation, error) {
	tx, err := s.st.Pool().Begin(ctx)
	if err != nil {
		return nil, err
	}
	defer tx.Rollback(ctx) //nolint:errcheck // no-op after commit

	existing, err := s.st.ClaimEnvelope(ctx, tx, key, "DeleteVm", "v1", "vm", req.GetName(), hash)
	if err != nil {
		return nil, err
	}
	if existing != nil {
		if err := tx.Commit(ctx); err != nil {
			return nil, err
		}
		return operationToProto(existing), nil
	}

	vm, err := s.st.GetVM(ctx, tx, req.GetName())
	if err != nil {
		return nil, err
	}
	dead, err := s.st.TombstoneVM(ctx, tx, vm.ID, vm.ResourceVersion)
	if err != nil {
		return nil, err
	}
	op, err := s.st.CreateOperation(ctx, tx, &store.Operation{
		ID: uuid.New(), ResourceType: "vm", ResourceID: dead.ID,
		ResourceName: dead.Name, Verb: "DELETE",
		TargetRevision: dead.DesiredRevision,
		DeadlineBudget: deleteDeadline,
	})
	if err != nil {
		return nil, err
	}
	if err := s.st.CompleteEnvelope(ctx, tx, key, op.ID); err != nil {
		return nil, err
	}
	if err := tx.Commit(ctx); err != nil {
		return nil, err
	}
	s.log.InfoContext(ctx, "vm delete accepted",
		"vm", dead.Name, "operation", op.ID, "target_revision", op.TargetRevision)
	return operationToProto(op), nil
}

func (s *Server) GetVm(ctx context.Context, req *vmcv1.GetVmRequest) (*vmcv1.VirtualMachine, error) {
	vm, err := s.st.GetVM(ctx, nil, req.GetName())
	if err != nil {
		return nil, mapStoreErr(err)
	}
	return vmToProto(vm), nil
}

func (s *Server) ListVms(ctx context.Context, req *vmcv1.ListVmsRequest) (*vmcv1.ListVmsResponse, error) {
	limit := int(req.GetPageSize())
	if limit <= 0 || limit > 500 {
		limit = 100
	}
	// Fetch limit+1: the extra row is the only honest evidence that a next
	// page exists (PR 4-8 review triage — no spurious or missing tokens).
	vms, err := s.st.ListVMs(ctx, nil, req.GetPageToken(), limit+1)
	if err != nil {
		return nil, mapStoreErr(err)
	}
	resp := &vmcv1.ListVmsResponse{}
	for i, vm := range vms {
		if i == limit {
			resp.NextPageToken = vms[limit-1].Name
			break
		}
		resp.Vms = append(resp.Vms, vmToProto(vm))
	}
	return resp, nil
}

func (s *Server) GetOperation(ctx context.Context, req *vmcv1.GetOperationRequest) (*vmcv1.Operation, error) {
	id, err := uuid.Parse(req.GetId())
	if err != nil {
		return nil, status.Error(codes.InvalidArgument, "operation id must be a UUID")
	}
	op, err := s.st.GetOperation(ctx, nil, id)
	if err != nil {
		return nil, mapStoreErr(err)
	}
	return operationToProto(op), nil
}

// WaitOperation polls until terminal or the requested wait elapses. The
// server caps the wait; clients keep their own defensive deadline (vmctl
// does).
func (s *Server) WaitOperation(ctx context.Context, req *vmcv1.WaitOperationRequest) (*vmcv1.Operation, error) {
	id, err := uuid.Parse(req.GetId())
	if err != nil {
		return nil, status.Error(codes.InvalidArgument, "operation id must be a UUID")
	}
	wait := 30 * time.Second
	if d := req.GetTimeout(); d != nil {
		if err := d.CheckValid(); err != nil {
			return nil, status.Error(codes.InvalidArgument, "timeout: "+err.Error())
		}
		if v := d.AsDuration(); v < 0 {
			return nil, status.Error(codes.InvalidArgument, "timeout must not be negative")
		} else if v > 0 {
			wait = min(v, 2*time.Minute)
		}
	}
	deadline := time.Now().Add(wait)
	tick := time.NewTicker(200 * time.Millisecond)
	defer tick.Stop()
	for {
		op, err := s.st.GetOperation(ctx, nil, id)
		if err != nil {
			return nil, mapStoreErr(err)
		}
		if op.Terminal() || time.Now().After(deadline) {
			return operationToProto(op), nil
		}
		select {
		case <-ctx.Done():
			return nil, status.FromContextError(ctx.Err()).Err()
		case <-tick.C:
		}
	}
}

// --- mapping ----------------------------------------------------------------

func mapStoreErr(err error) error {
	// Errors that already carry a gRPC status (e.g. validation inside the
	// winning transaction) pass through unchanged.
	if _, ok := status.FromError(err); ok {
		return err
	}
	switch {
	case errors.Is(err, context.Canceled), errors.Is(err, context.DeadlineExceeded):
		return status.FromContextError(err).Err()
	case errors.Is(err, store.ErrNotFound):
		return status.Error(codes.NotFound, err.Error())
	case errors.Is(err, store.ErrDuplicate):
		return status.Error(codes.AlreadyExists, err.Error())
	case errors.Is(err, store.ErrEnvelopeMismatch):
		return status.Error(codes.FailedPrecondition, err.Error())
	case errors.Is(err, store.ErrStaleWrite):
		return status.Error(codes.Aborted, err.Error())
	default:
		return status.Error(codes.Internal, err.Error())
	}
}

var phaseFromString = map[string]vmcv1.Phase{
	"PENDING":      vmcv1.Phase_PHASE_PENDING,
	"SCHEDULING":   vmcv1.Phase_PHASE_SCHEDULING,
	"PROVISIONING": vmcv1.Phase_PHASE_PROVISIONING,
	"RUNNING":      vmcv1.Phase_PHASE_RUNNING,
	"STOPPED":      vmcv1.Phase_PHASE_STOPPED,
	"UNKNOWN":      vmcv1.Phase_PHASE_UNKNOWN,
	"FAILED":       vmcv1.Phase_PHASE_FAILED,
	"DELETING":     vmcv1.Phase_PHASE_DELETING,
}

func vmToProto(vm *store.VM) *vmcv1.VirtualMachine {
	meta := &vmcv1.ResourceMeta{
		Id:              vm.ID.String(),
		Name:            vm.Name,
		SpecGeneration:  vm.SpecGeneration,
		ResourceVersion: vm.ResourceVersion,
		DesiredRevision: vm.DesiredRevision,
		CreateTime:      timestamppb.New(vm.CreatedAt),
		UpdateTime:      timestamppb.New(vm.UpdatedAt),
	}
	if vm.DeletedAt != nil {
		meta.DeleteTime = timestamppb.New(*vm.DeletedAt)
	}
	st := vm.Status
	if st == nil {
		st = &vmcv1.VmStatus{}
	}
	st.Phase = phaseFromString[vm.Phase]
	// Presentation only: a tombstoned row reads as DELETING regardless of
	// the reconciler-owned phase column — the API never WRITES phase (§6),
	// but the user deserves to see the deletion in flight.
	if vm.DeletedAt != nil {
		st.Phase = vmcv1.Phase_PHASE_DELETING
	}
	if vm.NodeName != nil {
		st.Node = *vm.NodeName
	}
	st.PlacementEpoch = vm.PlacementEpoch
	return &vmcv1.VirtualMachine{Meta: meta, Spec: vm.Spec, Status: st}
}

var opStateFromString = map[string]vmcv1.OperationState{
	"PENDING":           vmcv1.OperationState_OPERATION_STATE_PENDING,
	"RUNNING":           vmcv1.OperationState_OPERATION_STATE_RUNNING,
	"DONE":              vmcv1.OperationState_OPERATION_STATE_DONE,
	"FAILED":            vmcv1.OperationState_OPERATION_STATE_FAILED,
	"SUPERSEDED":        vmcv1.OperationState_OPERATION_STATE_SUPERSEDED,
	"DEADLINE_EXCEEDED": vmcv1.OperationState_OPERATION_STATE_DEADLINE_EXCEEDED,
}

var verbFromString = map[string]vmcv1.Verb{
	"CREATE":               vmcv1.Verb_VERB_CREATE,
	"UPDATE_POWER":         vmcv1.Verb_VERB_UPDATE_POWER,
	"DELETE":               vmcv1.Verb_VERB_DELETE,
	"SNAPSHOT_CREATE":      vmcv1.Verb_VERB_SNAPSHOT_CREATE,
	"SNAPSHOT_RESTORE":     vmcv1.Verb_VERB_SNAPSHOT_RESTORE,
	"ADMIN_RETRY_CLEANUP":  vmcv1.Verb_VERB_ADMIN_RETRY_CLEANUP,
	"ADMIN_CLEAR_RECOVERY": vmcv1.Verb_VERB_ADMIN_CLEAR_RECOVERY,
}

func operationToProto(op *store.Operation) *vmcv1.Operation {
	out := &vmcv1.Operation{
		Id:             op.ID.String(),
		ResourceType:   op.ResourceType,
		ResourceId:     op.ResourceID.String(),
		ResourceName:   op.ResourceName,
		Verb:           verbFromString[op.Verb],
		TargetRevision: op.TargetRevision,
		State:          opStateFromString[op.State],
		Error:          op.Error,
		Deadline:       timestamppb.New(op.Deadline),
		CreateTime:     timestamppb.New(op.CreatedAt),
	}
	if op.FinishedAt != nil {
		out.FinishTime = timestamppb.New(*op.FinishedAt)
	}
	return out
}
