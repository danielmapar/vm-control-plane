# Failpoint manifest

Every crash boundary in the failure matrix (plan §7) is a named failpoint in
`internal/faults.Catalog`; `faults.Hit` panics on undeclared IDs, so this
manifest cannot silently drift from the code. **New failpoints land with the
PR that introduces their boundary.** Armed via:

```
VMC_FAILPOINTS="controller.after-assign=crash;agent.report=error:boom;x=hang:2s;y=pause"
```

| ID | Cut point | Invariant under crash | Owned by |
|---|---|---|---|
| `api.after-envelope` | after the envelope insert commits, before the caller sees the operation | replay returns the original operation; no duplicate VM | store/api PRs |
| `controller.after-claim` | claim minted, before transition work | lease expiry re-exposes the row; no side effects | claim-queue PR |
| `controller.before-complete` | transition computed, before the guarded completion tx | nothing durable changed; rescan redoes exactly once | claim-queue PR |

Actions: `crash` (exit 137 — the kill -9 analog), `error[:msg]` (retry-path
injection), `hang[:dur]` (hung-call containment), `pause` (in-process
barrier; released by the test).
