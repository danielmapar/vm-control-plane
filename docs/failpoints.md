# Failpoint manifest

Every crash boundary in the failure matrix (plan §7) is a named failpoint in
`internal/faults.Catalog`; `faults.Hit` panics on undeclared IDs and
`faults.Load` rejects unknown IDs/actions, so a typo'd failpoint fails loud
rather than silently doing nothing. **New failpoints land with the PR that
introduces their boundary.** Armed via:

```
VMC_FAILPOINTS="controller.before-complete=crash;api.after-envelope=error:boom;x=hang:2s;y=pause"
```

The table body below is **generated from the Catalog** by `faults.Manifest()`
and verified verbatim by `TestManifestMatchesDoc` — the docs cannot drift
from the code.

| ID | Cut point | Invariant under crash |
|---|---|---|
| `api.after-envelope` | after the idempotency envelope insert commits, before the caller sees the operation | a replay returns the original operation; no duplicate VM row |
| `controller.after-claim` | after a claim is minted, before any transition work | lease expiry makes the row reclaimable; no side effects exist |
| `controller.before-complete` | transition computed, before the guarded completion tx commits | nothing durable changed; rescan redoes the work exactly once |
| `controller.before-finalize` | teardown proven, before the finalization tx (row removal + op) commits | the Deleting row persists and finalization is redone exactly once |

Actions: `crash` (exit 137 — the controlled-crash analog), `error[:msg]`
(retry-path injection), `hang[:dur]` (hung-call containment), `pause`
(in-process barrier; released by the test).
