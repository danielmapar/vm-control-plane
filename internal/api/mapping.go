package api

import (
	"context"
	"errors"

	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
	"google.golang.org/protobuf/types/known/timestamppb"

	"github.com/sigtunnel/vm-control-plane/internal/store"
	vmcv1 "github.com/sigtunnel/vm-control-plane/proto/vmc/v1"
)

// mapStoreErr translates store sentinels to gRPC status codes. Errors that
// already carry a status (e.g. validation inside a winning transaction) pass
// through unchanged.
func mapStoreErr(err error) error {
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

var phaseToProto = map[store.Phase]vmcv1.Phase{
	store.PhasePending:      vmcv1.Phase_PHASE_PENDING,
	store.PhaseScheduling:   vmcv1.Phase_PHASE_SCHEDULING,
	store.PhaseProvisioning: vmcv1.Phase_PHASE_PROVISIONING,
	store.PhaseRunning:      vmcv1.Phase_PHASE_RUNNING,
	store.PhaseStopped:      vmcv1.Phase_PHASE_STOPPED,
	store.PhaseUnknown:      vmcv1.Phase_PHASE_UNKNOWN,
	store.PhaseFailed:       vmcv1.Phase_PHASE_FAILED,
	store.PhaseDeleting:     vmcv1.Phase_PHASE_DELETING,
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
	st.Phase = phaseToProto[vm.Phase]
	// Presentation only: a tombstoned row reads as DELETING regardless of the
	// reconciler-owned phase column. The API never writes phase, but the user
	// deserves to see the deletion in flight.
	if vm.DeletedAt != nil {
		st.Phase = vmcv1.Phase_PHASE_DELETING
	}
	if vm.NodeName != nil {
		st.Node = *vm.NodeName
	}
	st.PlacementEpoch = vm.PlacementEpoch
	return &vmcv1.VirtualMachine{Meta: meta, Spec: vm.Spec, Status: st}
}

// observedFromProto maps a reported ObservedState enum to its stored value.
// The zero value of the enum (unspecified) is absent from the map, so callers
// can reject it.
var observedFromProto = map[vmcv1.ObservedState]store.ObservedState{
	vmcv1.ObservedState_OBSERVED_STATE_RUNNING: store.ObservedRunning,
	vmcv1.ObservedState_OBSERVED_STATE_SHUTOFF: store.ObservedShutoff,
	vmcv1.ObservedState_OBSERVED_STATE_ABSENT:  store.ObservedAbsent,
}

var opStateToProto = map[store.OperationState]vmcv1.OperationState{
	store.OpPending:          vmcv1.OperationState_OPERATION_STATE_PENDING,
	store.OpRunning:          vmcv1.OperationState_OPERATION_STATE_RUNNING,
	store.OpDone:             vmcv1.OperationState_OPERATION_STATE_DONE,
	store.OpFailed:           vmcv1.OperationState_OPERATION_STATE_FAILED,
	store.OpSuperseded:       vmcv1.OperationState_OPERATION_STATE_SUPERSEDED,
	store.OpDeadlineExceeded: vmcv1.OperationState_OPERATION_STATE_DEADLINE_EXCEEDED,
}

var verbToProto = map[store.Verb]vmcv1.Verb{
	store.VerbCreate:             vmcv1.Verb_VERB_CREATE,
	store.VerbUpdatePower:        vmcv1.Verb_VERB_UPDATE_POWER,
	store.VerbDelete:             vmcv1.Verb_VERB_DELETE,
	store.VerbSnapshotCreate:     vmcv1.Verb_VERB_SNAPSHOT_CREATE,
	store.VerbSnapshotRestore:    vmcv1.Verb_VERB_SNAPSHOT_RESTORE,
	store.VerbAdminRetryCleanup:  vmcv1.Verb_VERB_ADMIN_RETRY_CLEANUP,
	store.VerbAdminClearRecovery: vmcv1.Verb_VERB_ADMIN_CLEAR_RECOVERY,
}

func operationToProto(op *store.Operation) *vmcv1.Operation {
	out := &vmcv1.Operation{
		Id:             op.ID.String(),
		ResourceType:   op.ResourceType,
		ResourceId:     op.ResourceID.String(),
		ResourceName:   op.ResourceName,
		Verb:           verbToProto[op.Verb],
		TargetRevision: op.TargetRevision,
		State:          opStateToProto[op.State],
		Error:          op.Error,
		Deadline:       timestamppb.New(op.Deadline),
		CreateTime:     timestamppb.New(op.CreatedAt),
	}
	if op.FinishedAt != nil {
		out.FinishTime = timestamppb.New(*op.FinishedAt)
	}
	return out
}
