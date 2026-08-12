package api_test

import (
	"context"
	"log/slog"
	"os"
	"testing"

	"github.com/google/uuid"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	"github.com/sigtunnel/vm-control-plane/internal/api"
	"github.com/sigtunnel/vm-control-plane/internal/store"
	"github.com/sigtunnel/vm-control-plane/internal/store/pgtest"
	vmcv1 "github.com/sigtunnel/vm-control-plane/proto/vmc/v1"
)

func TestMain(m *testing.M) { os.Exit(pgtest.Main(m)) }

func newServer(t *testing.T) *api.Server {
	t.Helper()
	pool := pgtest.NewDB(t)
	return api.NewServer(store.New(pool), slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: slog.LevelWarn})))
}

func createReq(name string) *vmcv1.CreateVmRequest {
	return &vmcv1.CreateVmRequest{
		IdempotencyKey: uuid.NewString(),
		Name:           name,
		Spec: &vmcv1.VmSpec{
			Cpus: 2, MemoryBytes: 1 << 30, Image: "ubuntu-24.04", RootDiskBytes: 2 << 30,
		},
	}
}

func TestCreateReplayIdempotent(t *testing.T) {
	s := newServer(t)
	ctx := context.Background()
	req := createReq("web-1")

	op1, err := s.CreateVm(ctx, req)
	if err != nil {
		t.Fatalf("create: %v", err)
	}
	// Same key, same request — the demo-7 case: 20 replays, one VM.
	for i := 0; i < 20; i++ {
		op, err := s.CreateVm(ctx, req)
		if err != nil {
			t.Fatalf("replay %d: %v", i, err)
		}
		if op.Id != op1.Id {
			t.Fatalf("replay %d returned different operation", i)
		}
	}
	list, err := s.ListVms(ctx, &vmcv1.ListVmsRequest{})
	if err != nil || len(list.Vms) != 1 {
		t.Fatalf("want exactly 1 vm, got %d (%v)", len(list.Vms), err)
	}
}

func TestCreateSameKeyDifferentRequestRejected(t *testing.T) {
	s := newServer(t)
	ctx := context.Background()
	req := createReq("web-1")
	if _, err := s.CreateVm(ctx, req); err != nil {
		t.Fatal(err)
	}

	altered := createReq("web-2")
	altered.IdempotencyKey = req.IdempotencyKey
	_, err := s.CreateVm(ctx, altered)
	if status.Code(err) != codes.FailedPrecondition {
		t.Fatalf("want FailedPrecondition, got %v", err)
	}
}

func TestCreateValidation(t *testing.T) {
	s := newServer(t)
	ctx := context.Background()

	bad := createReq("Bad_Name")
	if _, err := s.CreateVm(ctx, bad); status.Code(err) != codes.InvalidArgument {
		t.Fatalf("bad name: want InvalidArgument, got %v", err)
	}
	noCPU := createReq("ok-name")
	noCPU.Spec.Cpus = 0
	if _, err := s.CreateVm(ctx, noCPU); status.Code(err) != codes.InvalidArgument {
		t.Fatalf("cpus=0: want InvalidArgument, got %v", err)
	}
	noKey := createReq("ok-name")
	noKey.IdempotencyKey = "not-a-uuid"
	if _, err := s.CreateVm(ctx, noKey); status.Code(err) != codes.InvalidArgument {
		t.Fatalf("bad key: want InvalidArgument, got %v", err)
	}
}

func TestDuplicateNameDistinctKeys(t *testing.T) {
	s := newServer(t)
	ctx := context.Background()
	if _, err := s.CreateVm(ctx, createReq("dup")); err != nil {
		t.Fatal(err)
	}
	_, err := s.CreateVm(ctx, createReq("dup"))
	if status.Code(err) != codes.AlreadyExists {
		t.Fatalf("want AlreadyExists, got %v", err)
	}
}

func TestDeleteTombstonesAndReplays(t *testing.T) {
	s := newServer(t)
	ctx := context.Background()
	if _, err := s.CreateVm(ctx, createReq("del-1")); err != nil {
		t.Fatal(err)
	}

	del := &vmcv1.DeleteVmRequest{IdempotencyKey: uuid.NewString(), Name: "del-1"}
	op1, err := s.DeleteVm(ctx, del)
	if err != nil {
		t.Fatalf("delete: %v", err)
	}
	op2, err := s.DeleteVm(ctx, del)
	if err != nil || op2.Id != op1.Id {
		t.Fatalf("delete replay: %v (ids %s vs %s)", err, op1.Id, op2.Id)
	}

	vm, err := s.GetVm(ctx, &vmcv1.GetVmRequest{Name: "del-1"})
	if err != nil {
		t.Fatalf("tombstoned vm must remain readable: %v", err)
	}
	if vm.Status.Phase != vmcv1.Phase_PHASE_DELETING || vm.Meta.DeleteTime == nil {
		t.Fatalf("tombstone not visible: %+v", vm.Status)
	}
	if vm.Meta.DesiredRevision != 2 {
		t.Fatalf("delete must bump desired_revision to 2, got %d", vm.Meta.DesiredRevision)
	}
}

func TestDeleteMissingVM(t *testing.T) {
	s := newServer(t)
	ctx := context.Background()
	_, err := s.DeleteVm(ctx, &vmcv1.DeleteVmRequest{IdempotencyKey: uuid.NewString(), Name: "ghost"})
	if status.Code(err) != codes.NotFound {
		t.Fatalf("want NotFound, got %v", err)
	}
}

func TestOperationLifecycleVisible(t *testing.T) {
	s := newServer(t)
	ctx := context.Background()
	op, err := s.CreateVm(ctx, createReq("op-1"))
	if err != nil {
		t.Fatal(err)
	}
	got, err := s.GetOperation(ctx, &vmcv1.GetOperationRequest{Id: op.Id})
	if err != nil {
		t.Fatal(err)
	}
	if got.State != vmcv1.OperationState_OPERATION_STATE_PENDING {
		t.Fatalf("fresh operation state: %v", got.State)
	}
	if got.Verb != vmcv1.Verb_VERB_CREATE || got.TargetRevision != 1 {
		t.Fatalf("operation identity: %+v", got)
	}
	if got.Deadline == nil {
		t.Fatal("operation must carry its per-verb deadline")
	}
}

func TestListPagination(t *testing.T) {
	s := newServer(t)
	ctx := context.Background()
	for _, n := range []string{"pa", "pb", "pc"} {
		if _, err := s.CreateVm(ctx, createReq(n)); err != nil {
			t.Fatal(err)
		}
	}
	p1, err := s.ListVms(ctx, &vmcv1.ListVmsRequest{PageSize: 2})
	if err != nil || len(p1.Vms) != 2 || p1.NextPageToken == "" {
		t.Fatalf("page1: %v %+v", err, p1)
	}
	p2, err := s.ListVms(ctx, &vmcv1.ListVmsRequest{PageSize: 2, PageToken: p1.NextPageToken})
	if err != nil || len(p2.Vms) != 1 {
		t.Fatalf("page2: %v %+v", err, p2)
	}
}
