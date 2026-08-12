// Package faults is the failure-injection registry (plan §10). Every crash
// boundary in the §7 failure matrix is a named failpoint declared in the
// Catalog; tests (and the chaos scripts) arm them via the environment:
//
//	VMC_FAILPOINTS="controller.after-assign=crash;agent.report=error:boom;x=hang:2s;y=pause"
//
// Actions:
//
//	crash        os.Exit(137) at the failpoint — the kill -9 analog that a
//	             subprocess harness restarts and asserts convergence after.
//	error[:msg]  return an injected error, exercising the retry path.
//	hang[:dur]   block for dur (default 30s) or until the context ends —
//	             the hung-driver-call case (matrix row 20/22).
//	pause        park until the test calls Release(id) — the in-process
//	             harness's deterministic barrier.
//
// A failpoint that is not armed costs one map lookup on a nil map. Hit
// panics on IDs missing from the Catalog so the failpoint manifest
// (docs/failpoints.md) can never silently drift from the code.
package faults

import (
	"context"
	"fmt"
	"os"
	"strings"
	"sync"
	"time"
)

// Point documents one failpoint: where it cuts and what invariant its
// tests assert. The manifest test renders this table into docs.
type Point struct {
	ID        string
	Cut       string // exact boundary, e.g. "after the placement tx commits, before intent visibility"
	Invariant string // what must hold when a crash lands here
}

// Catalog is the complete failpoint inventory. IDs are hierarchical:
// component.boundary. New failpoints land WITH the PR that introduces
// their boundary, never retroactively.
var Catalog = map[string]Point{
	"api.after-envelope": {
		ID:        "api.after-envelope",
		Cut:       "after the idempotency envelope insert commits, before the caller sees the operation",
		Invariant: "a replay returns the original operation; no duplicate VM row",
	},
	"controller.after-claim": {
		ID:        "controller.after-claim",
		Cut:       "after a claim is minted, before any transition work",
		Invariant: "lease expiry makes the row reclaimable; no side effects exist",
	},
	"controller.before-complete": {
		ID:        "controller.before-complete",
		Cut:       "transition computed, before the guarded completion tx commits",
		Invariant: "nothing durable changed; rescan redoes the work exactly once",
	},
}

type action struct {
	kind string // crash | error | hang | pause
	arg  string
}

var (
	mu     sync.Mutex
	armed  map[string]action
	pauses map[string]chan struct{}
)

func init() {
	Load(os.Getenv("VMC_FAILPOINTS"))
}

// Load (re)arms failpoints from a spec string. Tests use it directly;
// production processes inherit it from the environment at start.
func Load(spec string) {
	mu.Lock()
	defer mu.Unlock()
	armed = nil
	pauses = nil
	if spec == "" {
		return
	}
	armed = map[string]action{}
	pauses = map[string]chan struct{}{}
	for _, part := range strings.Split(spec, ";") {
		part = strings.TrimSpace(part)
		if part == "" {
			continue
		}
		id, act, ok := strings.Cut(part, "=")
		if !ok {
			continue
		}
		kind, arg, _ := strings.Cut(act, ":")
		armed[id] = action{kind: kind, arg: arg}
		if kind == "pause" {
			pauses[id] = make(chan struct{})
		}
	}
}

// Hit evaluates a failpoint. It panics on unknown IDs — the manifest and
// the code cannot drift apart silently.
func Hit(ctx context.Context, id string) error {
	if _, known := Catalog[id]; !known {
		panic(fmt.Sprintf("faults: %q is not in the Catalog — declare it (and its manifest row) in the PR that adds it", id))
	}
	mu.Lock()
	act, on := armed[id]
	pauseCh := pauses[id]
	mu.Unlock()
	if !on {
		return nil
	}

	switch act.kind {
	case "crash":
		fmt.Fprintf(os.Stderr, "faults: crash at %s\n", id)
		os.Exit(137)
	case "error":
		msg := act.arg
		if msg == "" {
			msg = "injected failure"
		}
		return fmt.Errorf("faults(%s): %s", id, msg)
	case "hang":
		d := 30 * time.Second
		if act.arg != "" {
			if parsed, err := time.ParseDuration(act.arg); err == nil {
				d = parsed
			}
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(d):
		}
	case "pause":
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-pauseCh:
		}
	}
	return nil
}

// Release unblocks a paused failpoint (the in-process barrier's other
// half). Releasing an unarmed or already-released pause is a no-op.
func Release(id string) {
	mu.Lock()
	defer mu.Unlock()
	if ch, ok := pauses[id]; ok {
		select {
		case <-ch:
		default:
			close(ch)
		}
	}
}
