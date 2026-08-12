# AI review triage — PRs 4–8

Reviewer: codex `gpt-5.6-sol` ultra; overall 4/10, "request changes." The
review examined the stack AT PR 8 — several findings were already resolved
by later PRs in the same stack (noted below). Triage per plan §13: accepted
fixes land in this triage PR; deferrals carry their destination; rejections
carry reasons.

## Accepted — fixed in this triage PR

| Finding | Fix |
|---|---|
| Operation deadlines used the API host's clock | `CreateOperation` now computes `deadline = clock_timestamp() + per-verb budget` in the INSERT; terminal timestamps use `clock_timestamp()`; expiry boundary is `<=` |
| Create validated before claiming the envelope (replays could fail new validation policy; altered requests hit validation instead of mismatch) | Hash-before-claim on a normalized CLONE (no caller mutation feeding the hash); validation runs only inside the winning transaction |
| `validateSpec` mutated the request (default power) before hashing | `normalizeSpec` on the clone; power validated against the known set; nil-UUID keys rejected |
| API wrote reconciler-owned `phase` on tombstone (writer-table violation) | `TombstoneVM` touches only `deleted_at`/`desired_revision`/`resource_version`; DELETING is *derived at presentation* from `delete_time` |
| Repeat tombstone bumped versions ("no-op" overstated) | True no-op now: zero-row guarded update + read-back; replay returns the row unchanged with no CAS conflict (tested) |
| Claim predicate would exclude RUNNING/STOPPED rows from drift repair | Predicate is now `next_attempt_at due AND lease free AND (phase <> FAILED OR tombstoned)` |
| `WaitOperation` swallowed context errors; cap semantics fuzzy | `status.FromContextError` on cancellation; `CheckValid`, negative rejection, `min(requested, 2m)` incl. equality |
| Pagination emitted spurious/missing tokens | limit+1 fetch; token only when the extra row exists |
| gRPC mapping wrapped status errors as Internal; no ctx mapping | Status errors pass through; context errors map to CANCELED/DEADLINE_EXCEEDED |
| `vmctl` had no key control and dropped pages | `--idempotency-key` flag, key printed before send; `list` follows continuation tokens with a repeated-token guard |
| pgtest: `CREATE DATABASE` identifier concatenation; port close-then-bind race; stolen-lock risk window | `pgx.Identifier.Sanitize()`; bind-failure retry with a fresh port; (lock-steal hardening deferred, below) |
| PR 4 acceptance (buf lint/generate) not enforced | CI `proto` job: buf lint + generate + clean-diff gate |

## Already resolved by later stack PRs

- **"ExpireOperations has no production caller"** — the reconciler loop
  (stack PR 16) sweeps it every tick, and the E2E suite exercises the loop.
- **"--role advertises a controller that does not exist"** — the controller
  exists as of stack PR 16 and is gated on the role flag.

## Deferred — destination named

- Status dual-representation (relational columns vs protojson `status`):
  consolidation to columns-canonical lands with the drift/status PR, which
  reworks status materialization anyway.
- Envelope→operation FK/uniqueness + CHECK constraints + terminal-row
  trigger + migration advisory lock + hash versioning column: hardening
  migration (0007) in the deletion-completion PR.
- protojson `DiscardUnknown` fidelity + full round-trip tests: with the
  power-update PR (first spec REwrite path — until then rows are written
  once).
- CLI rich output (`-o json`, full §6 fields), parseSize overflow guards,
  noun validation, op-wait retry budget: with the power/CLI PR.
- bufconn-transport API tests incl. cancellation + concurrency barriers:
  with the power PR's API surface change.
- pgtest extraction-lock true advisory locking: harness-hardening follow-up.

## Rejected — with reasons

- **"resource_version CAS on operations/envelopes rows"** — operations are
  append-then-terminalize with a state-guarded transition (`WHERE state IN
  (PENDING,RUNNING)`), which *is* the concurrency control for their only
  legal mutation; envelopes are insert-once + single completion under the
  same transaction as creation. A version column would add writes without
  adding a guarantee. Documented here as the deliberate exception to the
  all-rows rule (the plan's wording will be tightened in the next docs PR).
- **"ErrEnvelopeIncomplete narrative"** — partially accepted: the retry
  bound stays (a committed-then-rolled-back winner is real), but the
  reviewer is right that a *committed* NULL `operation_id` would be
  corruption; the hardening migration adds the constraint that makes it
  unrepresentable, at which point the retry loop only covers the rollback
  window.
