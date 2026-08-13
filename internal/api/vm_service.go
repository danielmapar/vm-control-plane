package api

import (
	"context"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
	"google.golang.org/protobuf/proto"

	"github.com/sigtunnel/vm-control-plane/internal/store"
	vmcv1 "github.com/sigtunnel/vm-control-plane/proto/vmc/v1"
)

// CreateVm claims the envelope, then inserts the VM, the operation, and the
// envelope completion in one transaction. A replay returns the original
// operation; a reused key with a different request is FAILED_PRECONDITION.
func (s *Server) CreateVm(ctx context.Context, req *vmcv1.CreateVmRequest) (*vmcv1.Operation, error) {
	key, err := parseIdempotencyKey(req.GetIdempotencyKey())
	if err != nil {
		return nil, err
	}
	// Normalize a clone (defaults applied without mutating the caller's
	// request), hash it, and claim the envelope before validation: a replay
	// must return its original operation even if validation policy changed
	// between attempts, and an altered request must hit the mismatch error
	// rather than a validation error.
	norm := proto.Clone(req).(*vmcv1.CreateVmRequest)
	norm.IdempotencyKey = ""
	normalizeSpec(norm.GetSpec())
	hash, err := canonicalHash(norm, func(proto.Message) {})
	if err != nil {
		return nil, status.Error(codes.Internal, err.Error())
	}
	name, spec := norm.GetName(), norm.GetSpec()

	return s.runIdempotent(false, func() (*vmcv1.Operation, error) {
		return s.withEnvelope(ctx, key, "CreateVm", name, hash, func(tx pgx.Tx) (*store.Operation, error) {
			// Validate inside the winning transaction; replays never reach here.
			if err := validateName(name); err != nil {
				return nil, status.Error(codes.InvalidArgument, err.Error())
			}
			if err := validateSpec(spec); err != nil {
				return nil, status.Error(codes.InvalidArgument, err.Error())
			}
			vm, err := s.st.CreateVM(ctx, tx, uuid.New(), name, spec)
			if err != nil {
				return nil, err
			}
			op, err := s.st.CreateOperation(ctx, tx, &store.Operation{
				ID: uuid.New(), ResourceType: "vm", ResourceID: vm.ID,
				ResourceName: vm.Name, Verb: store.VerbCreate,
				TargetRevision: vm.DesiredRevision, DeadlineBudget: createDeadline,
			})
			if err != nil {
				return nil, err
			}
			s.log.InfoContext(ctx, "vm create accepted",
				"vm", vm.Name, "operation", op.ID, "target_revision", op.TargetRevision)
			return op, nil
		})
	})
}

// UpdateVmPower flips power, the only mutable spec field in v0.1. Envelope
// discipline matches create; an optional resource_version precondition is
// honored (and, when supplied, never transparently retried).
func (s *Server) UpdateVmPower(ctx context.Context, req *vmcv1.UpdateVmPowerRequest) (*vmcv1.Operation, error) {
	key, err := parseIdempotencyKey(req.GetIdempotencyKey())
	if err != nil {
		return nil, err
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

	// With no user precondition, a lost CAS is a transparent retry; a
	// user-supplied precondition wins and surfaces the stale write instead.
	retryStale := req.GetExpectedResourceVersion() == 0

	return s.runIdempotent(retryStale, func() (*vmcv1.Operation, error) {
		return s.withEnvelope(ctx, key, "UpdateVmPower", req.GetName(), hash, func(tx pgx.Tx) (*store.Operation, error) {
			vm, err := s.st.GetVM(ctx, tx, req.GetName())
			if err != nil {
				return nil, err
			}
			if vm.DeletedAt != nil {
				return nil, status.Error(codes.FailedPrecondition, "vm is being deleted")
			}
			expect := vm.ResourceVersion
			if v := req.GetExpectedResourceVersion(); v != 0 {
				expect = v
			}
			spec := proto.Clone(vm.Spec).(*vmcv1.VmSpec)
			spec.Power = req.GetPower()
			updated, err := s.st.UpdateVMPower(ctx, tx, vm.ID, expect, spec)
			if err != nil {
				return nil, err
			}
			op, err := s.st.CreateOperation(ctx, tx, &store.Operation{
				ID: uuid.New(), ResourceType: "vm", ResourceID: updated.ID,
				ResourceName: updated.Name, Verb: store.VerbUpdatePower,
				TargetRevision: updated.DesiredRevision, DeadlineBudget: createDeadline,
			})
			if err != nil {
				return nil, err
			}
			s.log.InfoContext(ctx, "vm power update accepted",
				"vm", updated.Name, "power", req.GetPower().String(), "operation", op.ID, "target_revision", op.TargetRevision)
			return op, nil
		})
	})
}

// DeleteVm claims the envelope, then tombstones the VM (set-once) and records a
// DELETE operation in one transaction. The row persists until teardown
// finalizes: deletion is a desired state, not an absence.
func (s *Server) DeleteVm(ctx context.Context, req *vmcv1.DeleteVmRequest) (*vmcv1.Operation, error) {
	key, err := parseIdempotencyKey(req.GetIdempotencyKey())
	if err != nil {
		return nil, err
	}
	hash, err := canonicalHash(req, func(m proto.Message) {
		m.(*vmcv1.DeleteVmRequest).IdempotencyKey = ""
	})
	if err != nil {
		return nil, status.Error(codes.Internal, err.Error())
	}

	return s.runIdempotent(true, func() (*vmcv1.Operation, error) {
		return s.withEnvelope(ctx, key, "DeleteVm", req.GetName(), hash, func(tx pgx.Tx) (*store.Operation, error) {
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
				ResourceName: dead.Name, Verb: store.VerbDelete,
				TargetRevision: dead.DesiredRevision, DeadlineBudget: deleteDeadline,
			})
			if err != nil {
				return nil, err
			}
			s.log.InfoContext(ctx, "vm delete accepted",
				"vm", dead.Name, "operation", op.ID, "target_revision", op.TargetRevision)
			return op, nil
		})
	})
}

// GetVm returns one VM by name.
func (s *Server) GetVm(ctx context.Context, req *vmcv1.GetVmRequest) (*vmcv1.VirtualMachine, error) {
	vm, err := s.st.GetVM(ctx, nil, req.GetName())
	if err != nil {
		return nil, mapStoreErr(err)
	}
	return vmToProto(vm), nil
}

// ListVms returns a page of VMs. It fetches limit+1 rows: the extra row is the
// only honest evidence that a next page exists, so the page token is neither
// spurious nor missing.
func (s *Server) ListVms(ctx context.Context, req *vmcv1.ListVmsRequest) (*vmcv1.ListVmsResponse, error) {
	limit := int(req.GetPageSize())
	if limit <= 0 || limit > 500 {
		limit = 100
	}
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
