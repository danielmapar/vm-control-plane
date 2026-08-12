# AI review triage — implementation PRs 9–25

Reviewer: codex `gpt-5.6-sol` ultra; overall **3/10, request changes** — a
deep 70-finding pass focused on concurrency correctness in the SQL and the
agent, plus honesty of the "Learning notes." This is the most valuable
review of the project: it found real correctness holes that the
happy-path tests passed straight over. Triaged per plan §13; **the fixes in
this PR are the point of the whole exercise.**

## Accepted — fixed in this PR (with regression tests)

### Claim / transition core
- **[0] Expired claim could still commit.** The lease check was the *first*
  statement of `CompleteClaimTx`, leaving a stall window. Restructured to a
  two-phase protocol: `CompleteClaimTx` takes the row lock without releasing
  the claim; **`FinishClaim` is the LAST statement before commit** and
  re-checks token + unexpired lease. Every loop path (schedule, converge,
  unschedulable, delete, no-op) now calls it. Tests assert the stale worker
  loses *at FinishClaim*.
- **[2] Convergence overwrote fresh state.** `reconcileConvergence` now
  recomputes desired-vs-observed from the **fresh locked row** and aborts if
  a tombstone, power change, or newer revision arrived since the claim
  snapshot — a stale `DONE` can no longer clobber it.
- **[3] Tombstoned VM could be re-placed.** `PlaceVM` rechecks
  `deleted_at IS NULL` and `phase='PENDING'` **under the row lock**
  (`ErrPlacementPreconditions`); the loop requeues on it.

### Grants / evidence / teardown
- **[19] Granted-placement replay skipped authorization.** Grant replay now
  re-validates the current live session/lease exactly like the initial
  transition — a wrong/expired/superseded daemon can never get `Granted=true`.
- **[24] A teardown receipt could tear down a LIVE VM.** `MarkTeardownComplete`
  now requires the VM tombstoned **or** the epoch superseded, under the
  VM→placement lock order (`ErrTeardownNotPermitted`); regression-tested.
- **[10] Node-loss recovery restored from stale evidence.** Entering
  `UNKNOWN` clears the applied-revision/observed watermark, so a returning
  node requires a *fresh* report before re-converging.

### Agent
- **[27] Resync could resurrect a deleted VM.** The tombstone path removes the
  `lastApplied` entry *first*; a new per-VM **monotonic watermark** drops
  stale re-dispatches. New test drives 5 resync periods post-delete + a
  stale pre-delete redispatch and asserts the domain stays absent.
- **[28] "Latest-wins" was only last-arrival-wins.** The executor now gates on
  `(deleted, epoch, revision)`; strictly-older work is dropped, equal-key
  drift repair passes, and the watermark is revalidated after the
  `not_before` pause.
- **[30] Agent acted past its lease on a blackholed heartbeat.** Added a
  conservative local lease deadline (renewed only on a *successful*
  heartbeat, minus a safety margin); `handle`/`pollLoop` fail closed when it
  lapses — not only on an explicit `FailedPrecondition`.
- **[31] Rejected evidence still seeded mutation.** `lastApplied` advances
  only after *accepted* evidence.
- **[33] PID-file host lock had steal/empty-file races.** Replaced with an
  OS-held exclusive lock (`flock` on Unix, `LockFileEx` on Windows) that
  releases on process death — no stealing, no PID reuse ambiguity.

### Capacity / schema / infra
- **[16] Unchecked `uint64→int64` could bypass capacity** (a huge disk wraps
  negative and *decreases* `reserved_disk`). Checked conversions + range
  validation at the API boundary; `parseSize` overflow guard; CHECK
  constraints for positive quotas, non-negative counters, and
  `reserved_* <= quota`.
- **[15] Client-supplied lease was unbounded.** Server-capped at 60 s; empty
  host id / no nodes rejected.
- **[8] `terminalizeOps` matched only `resource_id`** (UUID reuse across
  types) and used transaction-start `now()`. Now filters `resource_type='vm'`
  and uses `clock_timestamp()`.
- **[12] Missing placement ledger row treated as teardown proof.** Deletion
  now **fails closed** on a missing row for a positive epoch (corruption →
  cleanup debt, not finalization).
- **[41] Migration runner could deadlock** (advisory lock on one conn, DDL
  through the pool). All DDL now runs through the locked connection.
- **[42]/[43] Failpoint manifest was not drift-proof.** `faults.Manifest()`
  generates the docs table; `TestManifestMatchesDoc` verifies it verbatim;
  `Load` now returns an error for unknown IDs/actions/durations and no longer
  leaks pause channels on reload.

### Honesty (the reviewer's meta-concern)
- **[46] "Real kill -9" was a voluntary `os.Exit(137)`.** Added
  `TestE2EControllerExternalKillRecovery`: the controller pauses at a
  failpoint, the harness detects the claim in the DB and calls
  `Process.Kill()`, and a clean restart recovers with exactly one placement.
  The exit-137 tests are relabeled as *controlled-crash* recovery.
- **[49] "Crash-mid-delete" renamed** to
  `TestE2EControllerCrashBeforeFinalization` (it crashes *after* the receipt).
- **[38]/[51] Tier-0 scope claims narrowed.** README and plan now state that
  fake-tier drift is power-state only and that shutdown attestations,
  next-boot/autostart drift, and the phase-aware auditor remain fake-tier
  work — "COMPLETE" downgraded to "the vertical slice."

## Deferred — destination named (with rationale)

These are real but land with the code they belong to, to keep this PR a
coherent concurrency-correctness pass rather than a rewrite:

- **[1] Full computed-from fencing** (spec + tombstone + epoch + evidence
  version as explicit claim inputs): the fixes above fence the *specific*
  fields each transition uses (tombstone/phase/revision under the lock);
  a uniform `computed_from` tuple on the claim row is a follow-up
  refactor, not a new guarantee.
- **[4] Bounded worker pool + lease renewal during long work**: matters when
  transitions call real drivers; lands with the libvirt executor (D8
  connection supervisor already designs the deadline story).
- **[5]/[6] Per-revision retry generations + generation-fenced admin
  cleanup**: with the `admin retry-cleanup` verb (not yet implemented).
- **[14]/[17] Authoritative topology snapshot on re-registration + label
  recheck in the reservation**: with a dedicated node-lifecycle PR.
- **[18]/[21]/[22]/[23] Fully-locked grant/report transactions + per-(vm,epoch)
  evidence watermark + Observe/Ensure serialization**: partially addressed
  (grant replay, report fencing, node-loss watermark); the remaining
  per-epoch watermark keying and substrate-observation versioning land with
  the real drivers, where Observe actually races Ensure.
- **[32]/[34]/[35]/[44]/[45]/[50] Goroutine lifecycle (errgroup/join),
  registration retry, supervisor health-checks + child containment**:
  robustness hardening, with the observability/hardening PR (plan M4).
- **[39]/[40] Deeper migration triggers** (deferred non-null envelope link,
  transition-legality constraints): with a schema-hardening follow-up.
- **[7] Deadline-precedence in terminalizing SQL**: the deadline sweep and
  explicit terminalization can still race the *label* (DONE vs
  DEADLINE_EXCEEDED); both are terminal and immutable-once-set, so no
  incorrect state results — encoding strict precedence is a polish item.

## Rejected — with reasons

- **[36] Fake-driver global mutex / context** — the fake exists to be
  deterministic and simple; its lock contention is not evidence about the
  real driver's isolation, and adding per-VM locking to a test double
  inverts the cost/benefit. The concern it points at (real driver isolation)
  is a real-driver test, tracked there.
- **"resource_version CAS on operations/envelopes"** (carried from the 4–8
  review) — reaffirmed: operations use a state-guarded transition and
  envelopes are insert-once; a version column adds writes without a
  guarantee. Documented as the deliberate exception.
