// Package e2e_test is the BLACK-BOX harness (plan §10): real binaries,
// real processes, real kill -9. The in-process tests elsewhere step through
// barriers; these prove the same claims with nothing shared but the
// database and the wire.
package e2e_test

import (
	"context"
	"errors"
	"fmt"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"sync"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials/insecure"
	"google.golang.org/protobuf/types/known/durationpb"

	"github.com/sigtunnel/vm-control-plane/internal/store/pgtest"
	vmcv1 "github.com/sigtunnel/vm-control-plane/proto/vmc/v1"
)

func TestMain(m *testing.M) { os.Exit(pgtest.Main(m)) }

var (
	buildOnce sync.Once
	binDir    string
	buildErr  error
)

// binaries compiles the real cmd/ binaries once per test run.
func binaries(t *testing.T) string {
	t.Helper()
	buildOnce.Do(func() {
		binDir, buildErr = os.MkdirTemp("", "vmc-e2e-bin-*")
		if buildErr != nil {
			return
		}
		cmd := exec.Command("go", "build", "-o", binDir+string(os.PathSeparator), "./cmd/...")
		cmd.Dir = repoRoot()
		out, err := cmd.CombinedOutput()
		if err != nil {
			buildErr = fmt.Errorf("build: %v\n%s", err, out)
		}
	})
	if buildErr != nil {
		t.Fatal(buildErr)
	}
	return binDir
}

func repoRoot() string {
	dir, _ := os.Getwd()
	for {
		if _, err := os.Stat(filepath.Join(dir, "go.mod")); err == nil {
			return dir
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			return ""
		}
		dir = parent
	}
}

func bin(dir, name string) string {
	if runtime.GOOS == "windows" {
		return filepath.Join(dir, name+".exe")
	}
	return filepath.Join(dir, name)
}

type proc struct {
	cmd *exec.Cmd
}

func start(t *testing.T, path string, env []string, args ...string) *proc {
	t.Helper()
	cmd := exec.Command(path, args...)
	cmd.Env = append(os.Environ(), env...)
	cmd.Stdout, cmd.Stderr = os.Stderr, os.Stderr
	if err := cmd.Start(); err != nil {
		t.Fatalf("start %s: %v", path, err)
	}
	p := &proc{cmd: cmd}
	t.Cleanup(func() { p.kill() })
	return p
}

func (p *proc) kill() {
	if p.cmd.Process != nil {
		_ = p.cmd.Process.Kill()
		_, _ = p.cmd.Process.Wait()
	}
}

// waitExit blocks until the process exits and returns its exit code.
func (p *proc) waitExit(t *testing.T, within time.Duration) int {
	t.Helper()
	done := make(chan int, 1)
	go func() {
		state, _ := p.cmd.Process.Wait()
		if state == nil {
			done <- -1
			return
		}
		done <- state.ExitCode()
	}()
	select {
	case code := <-done:
		return code
	case <-time.After(within):
		t.Fatal("process did not exit in time")
		return -1
	}
}

func waitCond(t *testing.T, what string, within time.Duration, cond func() bool) {
	t.Helper()
	deadline := time.Now().Add(within)
	for time.Now().Before(deadline) {
		if cond() {
			return
		}
		time.Sleep(50 * time.Millisecond)
	}
	t.Fatalf("timed out waiting for %s", what)
}

func freePort(t *testing.T) int {
	t.Helper()
	l, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer l.Close() //nolint:errcheck // probe listener
	return l.Addr().(*net.TCPAddr).Port
}

//nolint:unparam // timeout varies as slower scenarios land
func waitServing(t *testing.T, addr string, within time.Duration) {
	t.Helper()
	deadline := time.Now().Add(within)
	for time.Now().Before(deadline) {
		c, err := net.DialTimeout("tcp", addr, 500*time.Millisecond)
		if err == nil {
			_ = c.Close()
			return
		}
		time.Sleep(100 * time.Millisecond)
	}
	t.Fatalf("%s never started serving", addr)
}

func client(t *testing.T, addr string) (vmcv1.VMServiceClient, vmcv1.OperationServiceClient) {
	t.Helper()
	conn, err := grpc.NewClient(addr, grpc.WithTransportCredentials(insecure.NewCredentials()))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = conn.Close() })
	return vmcv1.NewVMServiceClient(conn), vmcv1.NewOperationServiceClient(conn)
}

func createReq(name string, cpus uint32) *vmcv1.CreateVmRequest {
	return &vmcv1.CreateVmRequest{
		IdempotencyKey: uuid.NewString(),
		Name:           name,
		Spec: &vmcv1.VmSpec{
			Cpus: cpus, MemoryBytes: 1 << 30, Image: "ubuntu-24.04", RootDiskBytes: 2 << 30,
			Power: vmcv1.PowerState_POWER_STATE_RUNNING,
		},
	}
}

//nolint:unparam // timeout varies as slower scenarios land
func waitDone(t *testing.T, ops vmcv1.OperationServiceClient, id string, within time.Duration) *vmcv1.Operation {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), within+10*time.Second)
	defer cancel()
	deadline := time.Now().Add(within)
	for {
		op, err := ops.WaitOperation(ctx, &vmcv1.WaitOperationRequest{
			Id: id, Timeout: durationpb.New(2 * time.Second),
		})
		if err != nil {
			t.Fatalf("wait: %v", err)
		}
		switch op.GetState() {
		case vmcv1.OperationState_OPERATION_STATE_DONE:
			return op
		case vmcv1.OperationState_OPERATION_STATE_PENDING,
			vmcv1.OperationState_OPERATION_STATE_RUNNING:
			if time.Now().After(deadline) {
				t.Fatalf("operation %s not DONE in time: %v %s", id, op.GetState(), op.GetError())
			}
		default:
			t.Fatalf("operation %s terminalized unexpectedly: %v %s", id, op.GetState(), op.GetError())
		}
	}
}

// TestE2ECreateToRunningAcrossNodes: demo 6a — three VMs spread across the
// daemon's two logical nodes, all reaching RUNNING through the full wire
// path (API → loop → placement → grant → fake ensure → evidence → DONE).
func TestE2ECreateToRunningAcrossNodes(t *testing.T) {
	dir := binaries(t)
	dbURL := pgtest.NewDBURL(t)
	addr := fmt.Sprintf("127.0.0.1:%d", freePort(t))

	start(t, bin(dir, "control-plane"), nil, "--listen", addr, "--db-url", dbURL)
	waitServing(t, addr, 30*time.Second)
	start(t, bin(dir, "hypervisor-agent"), nil,
		"--server", addr, "--state-dir", t.TempDir(), "--nodes", "node-a,node-b", "--node-cpus", "4")

	vms, ops := client(t, addr)
	ctx := context.Background()

	nodes := map[string]bool{}
	for i := 1; i <= 3; i++ {
		name := fmt.Sprintf("web-%d", i)
		op, err := vms.CreateVm(ctx, createReq(name, 2))
		if err != nil {
			t.Fatalf("create %s: %v", name, err)
		}
		waitDone(t, ops, op.GetId(), 60*time.Second)
		vm, err := vms.GetVm(ctx, &vmcv1.GetVmRequest{Name: name})
		if err != nil || vm.GetStatus().GetPhase() != vmcv1.Phase_PHASE_RUNNING {
			t.Fatalf("%s: %v phase=%v", name, err, vm.GetStatus().GetPhase())
		}
		nodes[vm.GetStatus().GetNode()] = true
	}
	if len(nodes) != 2 {
		t.Fatalf("expected spread across both logical nodes, got %v", nodes)
	}
}

// TestE2EControllerExternalKillRecovery: matrix rows 3/4 black-box with a
// GENUINE external SIGKILL (not a voluntary exit). The controller pauses at
// a failpoint after claiming the VM; the harness detects the claim in the
// database and calls Process.Kill(); a clean restart converges the VM with
// exactly one placement. This is the honest kill-9 demonstration (the
// earlier exit-137 tests prove restart-recovery but the crash is voluntary).
func TestE2EControllerExternalKillRecovery(t *testing.T) {
	dir := binaries(t)
	dbURL := pgtest.NewDBURL(t)
	addr := fmt.Sprintf("127.0.0.1:%d", freePort(t))

	// Pause after claiming so the row is claimed but no transition committed.
	paused := start(t, bin(dir, "control-plane"),
		[]string{"VMC_FAILPOINTS=controller.after-claim=pause"},
		"--listen", addr, "--db-url", dbURL)
	waitServing(t, addr, 30*time.Second)
	start(t, bin(dir, "hypervisor-agent"), nil,
		"--server", addr, "--state-dir", t.TempDir(), "--nodes", "node-a,node-b")

	vms, ops := client(t, addr)
	req := createReq("xkill-1", 1)
	if _, err := vms.CreateVm(context.Background(), req); err != nil {
		t.Fatal(err)
	}

	// Wait until the DB shows the VM claimed (claim_token set), then KILL.
	db, err := pgxpool.New(context.Background(), dbURL)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	waitCond(t, "vm claimed", 20*time.Second, func() bool {
		var claimed bool
		_ = db.QueryRow(context.Background(),
			`SELECT claim_token IS NOT NULL FROM vms WHERE name='xkill-1'`).Scan(&claimed)
		return claimed
	})
	if err := paused.cmd.Process.Kill(); err != nil {
		t.Fatalf("external kill: %v", err)
	}
	_, _ = paused.cmd.Process.Wait()

	// Restart clean; the abandoned claim's lease lapses and the rescan
	// re-drives the VM to Running with exactly one placement.
	start(t, bin(dir, "control-plane"), nil, "--listen", addr, "--db-url", dbURL)
	waitServing(t, addr, 30*time.Second)
	vms2, ops2 := client(t, addr)
	_ = ops
	op, err := vms2.CreateVm(context.Background(), req) // same key → original op
	if err != nil {
		t.Fatal(err)
	}
	waitDone(t, ops2, op.GetId(), 60*time.Second)
	vm, err := vms2.GetVm(context.Background(), &vmcv1.GetVmRequest{Name: "xkill-1"})
	if err != nil || vm.GetStatus().GetPhase() != vmcv1.Phase_PHASE_RUNNING {
		t.Fatalf("post-kill recovery: %v phase=%v", err, vm.GetStatus().GetPhase())
	}
	if vm.GetStatus().GetPlacementEpoch() != 1 {
		t.Fatalf("exactly one placement expected, epoch=%d", vm.GetStatus().GetPlacementEpoch())
	}
}

// TestE2EControllerCrashRecovery: matrix rows 3/4 with a voluntary exit-137
// crash (a controlled crash, not an external signal — see the external-kill
// test above for a genuine SIGKILL). Proves restart-rescan recovery.
func TestE2EControllerCrashRecovery(t *testing.T) {
	dir := binaries(t)
	dbURL := pgtest.NewDBURL(t)
	addr := fmt.Sprintf("127.0.0.1:%d", freePort(t))

	crashy := start(t, bin(dir, "control-plane"),
		[]string{"VMC_FAILPOINTS=controller.before-complete=crash"},
		"--listen", addr, "--db-url", dbURL)
	waitServing(t, addr, 30*time.Second)
	start(t, bin(dir, "hypervisor-agent"), nil,
		"--server", addr, "--state-dir", t.TempDir(), "--nodes", "node-a,node-b")

	vms, ops := client(t, addr)
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()

	req := createReq("crash-1", 1)
	if _, err := vms.CreateVm(ctx, req); err != nil {
		t.Fatalf("create: %v", err)
	}

	// The loop hits the failpoint and the process dies with 137.
	if code := crashy.waitExit(t, 30*time.Second); code != 137 {
		t.Fatalf("controller exit code = %d, want 137 (failpoint crash)", code)
	}

	// Restart WITHOUT the failpoint on the same database.
	start(t, bin(dir, "control-plane"), nil, "--listen", addr, "--db-url", dbURL)
	waitServing(t, addr, 30*time.Second)

	vms2, ops2 := client(t, addr)
	_ = ops
	// Replaying the create with the SAME idempotency key returns the
	// original operation (the client's crash-retry story).
	op, err := vms2.CreateVm(context.Background(), req)
	if err != nil {
		t.Fatalf("replay after crash: %v", err)
	}
	waitDone(t, ops2, op.GetId(), 60*time.Second)

	vm, err := vms2.GetVm(context.Background(), &vmcv1.GetVmRequest{Name: "crash-1"})
	if err != nil || vm.GetStatus().GetPhase() != vmcv1.Phase_PHASE_RUNNING {
		t.Fatalf("post-recovery: %v phase=%v", err, vm.GetStatus().GetPhase())
	}
	if vm.GetStatus().GetPlacementEpoch() != 1 {
		t.Fatalf("exactly one placement expected, epoch=%d", vm.GetStatus().GetPlacementEpoch())
	}
}

// TestE2EDeleteLifecycle: create → RUNNING → delete → operation DONE →
// row genuinely gone, while the DELETE operation stays queryable.
func TestE2EDeleteLifecycle(t *testing.T) {
	dir := binaries(t)
	dbURL := pgtest.NewDBURL(t)
	addr := fmt.Sprintf("127.0.0.1:%d", freePort(t))

	start(t, bin(dir, "control-plane"), nil, "--listen", addr, "--db-url", dbURL)
	waitServing(t, addr, 30*time.Second)
	start(t, bin(dir, "hypervisor-agent"), nil,
		"--server", addr, "--state-dir", t.TempDir(), "--nodes", "node-a,node-b")

	vms, ops := client(t, addr)
	ctx := context.Background()

	op, err := vms.CreateVm(ctx, createReq("del-e2e", 1))
	if err != nil {
		t.Fatal(err)
	}
	waitDone(t, ops, op.GetId(), 60*time.Second)

	delOp, err := vms.DeleteVm(ctx, &vmcv1.DeleteVmRequest{
		IdempotencyKey: uuid.NewString(), Name: "del-e2e",
	})
	if err != nil {
		t.Fatal(err)
	}
	waitDone(t, ops, delOp.GetId(), 60*time.Second)

	_, err = vms.GetVm(ctx, &vmcv1.GetVmRequest{Name: "del-e2e"})
	if err == nil {
		t.Fatal("vm row must be gone after finalization")
	}
	if !errors.Is(ctx.Err(), nil) {
		t.Fatal(ctx.Err())
	}
	// The DELETE operation outlives the resource.
	final, err := ops.GetOperation(ctx, &vmcv1.GetOperationRequest{Id: delOp.GetId()})
	if err != nil || final.GetState() != vmcv1.OperationState_OPERATION_STATE_DONE {
		t.Fatalf("delete operation must remain queryable: %v %+v", err, final)
	}
}

// TestE2EControllerCrashBeforeFinalization: matrix row 16 black-box — the
// controller crashes at the finalization boundary (teardown receipt already
// committed, row removal pending); a clean restart finalizes exactly once
// and the DELETE operation completes. (Agent-side unlink/fsync/receipt crash
// coverage is a real-driver concern, deferred with the qcow2 executor.)
func TestE2EControllerCrashBeforeFinalization(t *testing.T) {
	dir := binaries(t)
	dbURL := pgtest.NewDBURL(t)
	addr := fmt.Sprintf("127.0.0.1:%d", freePort(t))

	crashy := start(t, bin(dir, "control-plane"),
		[]string{"VMC_FAILPOINTS=controller.before-finalize=crash"},
		"--listen", addr, "--db-url", dbURL)
	waitServing(t, addr, 30*time.Second)
	start(t, bin(dir, "hypervisor-agent"), nil,
		"--server", addr, "--state-dir", t.TempDir(), "--nodes", "node-a,node-b")

	vms, ops := client(t, addr)
	ctx := context.Background()

	op, err := vms.CreateVm(ctx, createReq("cmd-1", 1))
	if err != nil {
		t.Fatal(err)
	}
	waitDone(t, ops, op.GetId(), 60*time.Second)

	delOp, err := vms.DeleteVm(ctx, &vmcv1.DeleteVmRequest{
		IdempotencyKey: uuid.NewString(), Name: "cmd-1",
	})
	if err != nil {
		t.Fatal(err)
	}

	// Teardown receipt arrives; finalization hits the failpoint → 137.
	if code := crashy.waitExit(t, 60*time.Second); code != 137 {
		t.Fatalf("controller exit = %d, want 137", code)
	}

	// The Deleting row survived the crash (teardown proven, not finalized).
	start(t, bin(dir, "control-plane"), nil, "--listen", addr, "--db-url", dbURL)
	waitServing(t, addr, 30*time.Second)

	vms2, ops2 := client(t, addr)
	waitDone(t, ops2, delOp.GetId(), 60*time.Second)
	if _, err := vms2.GetVm(context.Background(), &vmcv1.GetVmRequest{Name: "cmd-1"}); err == nil {
		t.Fatal("row must be gone after recovered finalization")
	}
}
