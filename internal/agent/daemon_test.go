package agent

import (
	"context"
	"path/filepath"
	"sync"
	"testing"
	"time"

	"google.golang.org/grpc"

	"github.com/sigtunnel/vm-control-plane/internal/driver/compute"
	"github.com/sigtunnel/vm-control-plane/internal/driver/compute/fake"
	vmcv1 "github.com/sigtunnel/vm-control-plane/proto/vmc/v1"
)

// fakeClient records AgentService calls; grants and reports flow through
// channels so tests can assert ordering without sleeps.
type fakeClient struct {
	vmcv1.AgentServiceClient // panic on unimplemented

	mu          sync.Mutex
	grantDenied string // deny reason; empty = grant
	grantCalls  int
	reports     chan *vmcv1.ReportRequest
	receipts    chan *vmcv1.TeardownReceiptRequest
	actionFails chan *vmcv1.ActionFailedRequest
}

func newFakeClient() *fakeClient {
	return &fakeClient{
		reports:     make(chan *vmcv1.ReportRequest, 16),
		receipts:    make(chan *vmcv1.TeardownReceiptRequest, 16),
		actionFails: make(chan *vmcv1.ActionFailedRequest, 16),
	}
}

func (f *fakeClient) RequestGrant(_ context.Context, _ *vmcv1.RequestGrantRequest, _ ...grpc.CallOption) (*vmcv1.RequestGrantResponse, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.grantCalls++
	if f.grantDenied != "" {
		return &vmcv1.RequestGrantResponse{Granted: false, Reason: f.grantDenied}, nil
	}
	return &vmcv1.RequestGrantResponse{Granted: true}, nil
}

func (f *fakeClient) Report(_ context.Context, r *vmcv1.ReportRequest, _ ...grpc.CallOption) (*vmcv1.ReportResponse, error) {
	f.reports <- r
	return &vmcv1.ReportResponse{}, nil
}

func (f *fakeClient) TeardownReceipt(_ context.Context, r *vmcv1.TeardownReceiptRequest, _ ...grpc.CallOption) (*vmcv1.TeardownReceiptResponse, error) {
	f.receipts <- r
	return &vmcv1.TeardownReceiptResponse{}, nil
}

func (f *fakeClient) ActionFailed(_ context.Context, r *vmcv1.ActionFailedRequest, _ ...grpc.CallOption) (*vmcv1.ActionFailedResponse, error) {
	f.actionFails <- r
	return &vmcv1.ActionFailedResponse{}, nil
}

func testDaemon(t *testing.T, client vmcv1.AgentServiceClient, drv compute.Driver) *Daemon {
	t.Helper()
	d := New(Config{
		HostID:   "h-test",
		StateDir: filepath.Join(t.TempDir(), "state"),
		Compute:  drv,
	}, client)
	d.sessions["node-a"] = &vmcv1.NodeSession{NodeName: "node-a", SessionId: "6e9f9d5e-0000-4000-8000-000000000001", SessionGeneration: 1}
	return d
}

//nolint:unparam // vmID varies as more scenarios land
func intent(vmID string, rev int64, deleted bool) *vmcv1.Intent {
	return &vmcv1.Intent{
		VmId: vmID, VmName: "vm-" + vmID, NodeName: "node-a",
		PlacementEpoch: 1, DesiredRevision: rev, Deleted: deleted,
		Spec: &vmcv1.VmSpec{Cpus: 1, MemoryBytes: 1 << 30, Image: "img", RootDiskBytes: 1 << 30,
			Power: vmcv1.PowerState_POWER_STATE_RUNNING},
	}
}

// TestGrantBeforeEnsureAndReport: the safety order — grant, then substrate,
// then evidence carrying the applied revision.
func TestGrantBeforeEnsureAndReport(t *testing.T) {
	fc := newFakeClient()
	drv := fake.New()
	d := testDaemon(t, fc, drv)
	ctx := context.Background()

	d.dispatch(ctx, intent("vm1", 1, false))

	select {
	case r := <-fc.reports:
		if r.GetAppliedRevision() != 1 || r.GetState() != vmcv1.ObservedState_OBSERVED_STATE_RUNNING {
			t.Fatalf("report: %+v", r)
		}
		if r.GetReportSeq() != 1 {
			t.Fatalf("first report seq = %d, want 1", r.GetReportSeq())
		}
	case <-time.After(5 * time.Second):
		t.Fatal("no report")
	}
	if fc.grantCalls != 1 {
		t.Fatalf("grant calls = %d, want 1", fc.grantCalls)
	}
	if drv.Count() != 1 {
		t.Fatalf("domains = %d, want 1", drv.Count())
	}
}

// TestGrantCachedPerEpoch: the second intent for the same epoch reuses the
// committed grant (idempotent exposure, no extra round-trips).
func TestGrantCachedPerEpoch(t *testing.T) {
	fc := newFakeClient()
	d := testDaemon(t, fc, fake.New())
	ctx := context.Background()

	d.dispatch(ctx, intent("vm1", 1, false))
	<-fc.reports
	d.dispatch(ctx, intent("vm1", 2, false))
	<-fc.reports

	if fc.grantCalls != 1 {
		t.Fatalf("grant must be cached per (vm, epoch): calls=%d", fc.grantCalls)
	}
}

// TestGrantDeniedNoSubstrateAction: a denied grant means the placement is
// not ours — the driver must never be touched (matrix row 7).
func TestGrantDeniedNoSubstrateAction(t *testing.T) {
	fc := newFakeClient()
	fc.grantDenied = "epoch-superseded"
	drv := fake.New()
	d := testDaemon(t, fc, drv)

	d.dispatch(context.Background(), intent("vm1", 1, false))
	time.Sleep(300 * time.Millisecond)

	if drv.Count() != 0 {
		t.Fatal("denied grant must prevent all substrate actions")
	}
	select {
	case r := <-fc.reports:
		t.Fatalf("unexpected report: %+v", r)
	default:
	}
}

// TestHaltStopsNewActions: a stale session (replacement daemon) halts the
// executor before any new substrate mutation (matrix row 10).
func TestHaltStopsNewActions(t *testing.T) {
	fc := newFakeClient()
	drv := fake.New()
	d := testDaemon(t, fc, drv)
	d.halted.Store(true)

	d.dispatch(context.Background(), intent("vm1", 1, false))
	time.Sleep(300 * time.Millisecond)

	if drv.Count() != 0 {
		t.Fatal("halted daemon must not touch the substrate")
	}
}

// TestTombstoneTeardownReceipt: a deleted intent drives teardown and its
// receipt; the report channel stays quiet.
func TestTombstoneTeardownReceipt(t *testing.T) {
	fc := newFakeClient()
	drv := fake.New()
	d := testDaemon(t, fc, drv)
	ctx := context.Background()

	// Provision first.
	d.dispatch(ctx, intent("vm1", 1, false))
	<-fc.reports

	d.dispatch(ctx, intent("vm1", 2, true))
	select {
	case r := <-fc.receipts:
		if r.GetPlacementEpoch() != 1 {
			t.Fatalf("receipt epoch: %+v", r)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("no teardown receipt")
	}
	if drv.Count() != 0 {
		t.Fatal("teardown must remove the domain")
	}
}

// TestEnsureFailureReportsActionFailed: driver errors flow to the durable
// server-side pacing, never to a local retry loop.
func TestEnsureFailureReportsActionFailed(t *testing.T) {
	fc := newFakeClient()
	drv := fake.New()
	drv.FailEnsure = func(compute.VMConfig) error { return context.DeadlineExceeded }
	d := testDaemon(t, fc, drv)

	d.dispatch(context.Background(), intent("vm1", 1, false))
	select {
	case af := <-fc.actionFails:
		if af.GetDesiredRevision() != 1 {
			t.Fatalf("action-failed: %+v", af)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("no ActionFailed")
	}
}

// TestHostLockExclusive: one daemon per host; stale locks from dead pids
// are stolen, live ones are not.
func TestHostLockExclusive(t *testing.T) {
	dir := t.TempDir()
	unlock, err := acquireHostLock(dir)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := acquireHostLock(dir); err == nil {
		t.Fatal("second daemon must not acquire a held host lock")
	}
	unlock()
	unlock2, err := acquireHostLock(dir)
	if err != nil {
		t.Fatalf("post-release acquire: %v", err)
	}
	unlock2()
}
