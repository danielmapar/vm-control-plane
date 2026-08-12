package store_test

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/sigtunnel/vm-control-plane/internal/store"
)

func host(id string) store.HostCapacity {
	return store.HostCapacity{HostID: id, CPUs: 16, MemoryBytes: 32 << 30, DiskBytes: 500 << 30}
}

func twoNodes() []store.NodeQuota {
	return []store.NodeQuota{
		{Name: "node-a", CPUs: 8, MemoryBytes: 16 << 30, DiskBytes: 200 << 30, Labels: map[string]string{"ssd": "true"}},
		{Name: "node-b", CPUs: 8, MemoryBytes: 16 << 30, DiskBytes: 200 << 30},
	}
}

func TestRegisterHostAndNodes(t *testing.T) {
	s := store.New(pgtestNewDB(t))
	ctx := context.Background()

	sessions, err := s.RegisterHost(ctx, host("host-1"), twoNodes(), time.Minute)
	if err != nil {
		t.Fatalf("register: %v", err)
	}
	if len(sessions) != 2 || sessions[0].Generation != 1 {
		t.Fatalf("sessions: %+v", sessions)
	}

	ready, err := s.ReadyNodes(ctx)
	if err != nil || len(ready) != 2 {
		t.Fatalf("ready: %v (%d)", err, len(ready))
	}
	if ready[0].Labels["ssd"] != "true" {
		t.Fatalf("labels lost: %+v", ready[0].Labels)
	}
}

// TestQuotaSumValidation: logical nodes cannot oversubscribe their host
// (v6 review finding — host-aggregate capacity).
func TestQuotaSumValidation(t *testing.T) {
	s := store.New(pgtestNewDB(t))
	ctx := context.Background()

	over := []store.NodeQuota{
		{Name: "node-a", CPUs: 12, MemoryBytes: 16 << 30, DiskBytes: 200 << 30},
		{Name: "node-b", CPUs: 12, MemoryBytes: 16 << 30, DiskBytes: 200 << 30}, // 24 > 16
	}
	_, err := s.RegisterHost(ctx, host("host-1"), over, time.Minute)
	if !errors.Is(err, store.ErrQuotaExceedsHost) {
		t.Fatalf("want ErrQuotaExceedsHost, got %v", err)
	}
}

// TestReRegistrationBumpsGeneration: a daemon restart mints new sessions
// with a HIGHER generation — the fence that orders reports across restarts.
func TestReRegistrationBumpsGeneration(t *testing.T) {
	s := store.New(pgtestNewDB(t))
	ctx := context.Background()

	first, err := s.RegisterHost(ctx, host("host-1"), twoNodes(), time.Minute)
	if err != nil {
		t.Fatal(err)
	}
	second, err := s.RegisterHost(ctx, host("host-1"), twoNodes(), time.Minute)
	if err != nil {
		t.Fatal(err)
	}
	if second[0].Generation != first[0].Generation+1 {
		t.Fatalf("generation: %d -> %d", first[0].Generation, second[0].Generation)
	}
	if second[0].SessionID == first[0].SessionID {
		t.Fatal("re-registration must mint a fresh session id")
	}
}

// TestCrossHostRebindRejected: matrix row 10 — a node identity cannot move
// to another host while its registration exists.
func TestCrossHostRebindRejected(t *testing.T) {
	s := store.New(pgtestNewDB(t))
	ctx := context.Background()

	if _, err := s.RegisterHost(ctx, host("host-1"), twoNodes(), time.Minute); err != nil {
		t.Fatal(err)
	}
	_, err := s.RegisterHost(ctx, host("host-2"), []store.NodeQuota{
		{Name: "node-a", CPUs: 2, MemoryBytes: 4 << 30, DiskBytes: 50 << 30},
	}, time.Minute)
	if !errors.Is(err, store.ErrHostMismatch) {
		t.Fatalf("want ErrHostMismatch, got %v", err)
	}
}

// TestStaleSessionHeartbeatRejected: the superseded daemon's heartbeat
// fails — its signal to halt substrate actions (§6.3).
func TestStaleSessionHeartbeatRejected(t *testing.T) {
	s := store.New(pgtestNewDB(t))
	ctx := context.Background()

	old, err := s.RegisterHost(ctx, host("host-1"), twoNodes(), time.Minute)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := s.RegisterHost(ctx, host("host-1"), twoNodes(), time.Minute); err != nil {
		t.Fatal(err) // replacement daemon
	}

	err = s.Heartbeat(ctx, old[0], time.Minute)
	if !errors.Is(err, store.ErrStaleSession) {
		t.Fatalf("stale heartbeat: want ErrStaleSession, got %v", err)
	}
}

// TestLeaseExpiryRemovesFromReady: NotReady is lease expiry, nothing else.
func TestLeaseExpiryRemovesFromReady(t *testing.T) {
	s := store.New(pgtestNewDB(t))
	ctx := context.Background()

	sess, err := s.RegisterHost(ctx, host("host-1"), twoNodes(), 100*time.Millisecond)
	if err != nil {
		t.Fatal(err)
	}
	time.Sleep(200 * time.Millisecond)

	ready, err := s.ReadyNodes(ctx)
	if err != nil || len(ready) != 0 {
		t.Fatalf("expired nodes still ready: %v (%d)", err, len(ready))
	}

	// A live heartbeat brings it back.
	if err := s.Heartbeat(ctx, sess[0], time.Minute); err != nil {
		t.Fatalf("heartbeat after expiry (same session, no takeover): %v", err)
	}
	ready, err = s.ReadyNodes(ctx)
	if err != nil || len(ready) != 1 {
		t.Fatalf("heartbeat did not restore readiness: %v (%d)", err, len(ready))
	}
}
