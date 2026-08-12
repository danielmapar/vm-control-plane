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
	"sort"
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
	"controller.before-finalize": {
		ID:        "controller.before-finalize",
		Cut:       "teardown proven, before the finalization tx (row removal + op) commits",
		Invariant: "the Deleting row persists and finalization is redone exactly once",
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
	if err := Load(os.Getenv("VMC_FAILPOINTS")); err != nil {
		panic(err) // a misconfigured failpoint spec must fail loud at startup
	}
}

// Load (re)arms failpoints from a spec string. Returns an error for any
// invalid specification — an unknown ID, an unknown action, or a malformed
// duration (batch-review finding [43]): a typo'd failpoint that silently
// does nothing is worse than a hard failure.
func Load(spec string) error {
	mu.Lock()
	defer mu.Unlock()
	// Close any live pause channels before discarding them so blocked
	// waiters are released rather than leaked.
	for _, ch := range pauses {
		select {
		case <-ch:
		default:
			close(ch)
		}
	}
	armed = nil
	pauses = nil
	if spec == "" {
		return nil
	}
	newArmed := map[string]action{}
	newPauses := map[string]chan struct{}{}
	for _, part := range strings.Split(spec, ";") {
		part = strings.TrimSpace(part)
		if part == "" {
			continue
		}
		id, act, ok := strings.Cut(part, "=")
		if !ok {
			return fmt.Errorf("faults: malformed spec %q (want id=action)", part)
		}
		if _, known := Catalog[id]; !known {
			return fmt.Errorf("faults: unknown failpoint id %q", id)
		}
		kind, arg, _ := strings.Cut(act, ":")
		switch kind {
		case "crash", "error", "pause":
		case "hang":
			if arg != "" {
				if _, err := time.ParseDuration(arg); err != nil {
					return fmt.Errorf("faults: bad hang duration %q: %w", arg, err)
				}
			}
		default:
			return fmt.Errorf("faults: unknown action %q for %q", kind, id)
		}
		newArmed[id] = action{kind: kind, arg: arg}
		if kind == "pause" {
			newPauses[id] = make(chan struct{})
		}
	}
	armed = newArmed
	pauses = newPauses
	return nil
}

// Manifest renders the Catalog as the docs table body — the source of the
// generated docs/failpoints.md, so the two cannot drift (finding [42]).
func Manifest() string {
	ids := make([]string, 0, len(Catalog))
	for id := range Catalog {
		ids = append(ids, id)
	}
	sort.Strings(ids)
	var b strings.Builder
	for _, id := range ids {
		p := Catalog[id]
		fmt.Fprintf(&b, "| `%s` | %s | %s |\n", p.ID, p.Cut, p.Invariant)
	}
	return b.String()
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
