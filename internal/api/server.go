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
	if err != nil {
		return nil, status.Error(codes.InvalidArgument, "idempotency_key must be a UUID (minted client-side before the first attempt)")
	}
	if err := validateName(req.GetName()); err != nil {
		return nil, status.Error(codes.InvalidArgument, err.Error())
	}
	if err := validateSpec(req.GetSpec()); err != nil {
		return nil, status.Error(codes.InvalidArgument, err.Error())
	}
	hash, err := canonicalHash(req, func(m proto.Message) {
		m.(*vmcv1.CreateVmRequest).IdempotencyKey = ""
	})
	if err != nil {
		return nil, status.Error(codes.Internal, err.Error())
	}

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

	vm, err := s.st.CreateVM(ctx, tx, uuid.New(), req.GetName(), req.GetSpec())
	if err != nil {
		return nil, err
	}
	op, err := s.st.CreateOperation(ctx, tx, &store.Operation{
		ID: uuid.New(), ResourceType: "vm", ResourceID: vm.ID,
		ResourceName: vm.Name, Verb: "CREATE",
		TargetRevision: vm.DesiredRevision,
		Deadline:       time.Now().Add(createDeadline),
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

// DeleteVm: envelope claim → tombstone (set-once) + operation, one
// transaction. The row persists until teardown finalizes — deletion is a
// desired state, not an absence.
func (s *Server) DeleteVm(ctx context.Context, req *vmcv1.DeleteVmRequest) (*vmcv1.Operation, error) {
	key, err := uuid.Parse(req.GetIdempotencyKey())
	if err != nil {
		return nil, status.Error(codes.InvalidArgument, "idempotency_key must be a UUID")
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
		Deadline:       time.Now().Add(deleteDeadline),
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
	vms, err := s.st.ListVMs(ctx, nil, req.GetPageToken(), int(req.GetPageSize()))
	if err != nil {
		return nil, mapStoreErr(err)
	}
	resp := &vmcv1.ListVmsResponse{}
	for _, vm := range vms {
		resp.Vms = append(resp.Vms, vmToProto(vm))
	}
	if n := len(vms); n > 0 && (req.GetPageSize() <= 0 || n == int(req.GetPageSize())) {
		resp.NextPageToken = vms[n-1].Name
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
		if v := d.AsDuration(); v > 0 && v < 2*time.Minute {
			wait = v
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
			return operationToProto(op), nil
		case <-tick.C:
		}
	}
}

// --- mapping ----------------------------------------------------------------

func mapStoreErr(err error) error {
	switch {
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
