# ADR-0003: Level-triggered reconciliation with a durable terminal-failure policy

**Status:** Accepted · **Date:** 2026-08-12

## Context

Edge-triggered designs (workflows, sagas) must never lose an event; that assumption dies at the first crash. The alternative — recompute the next convergent action from current desired + observed state on every pass — makes crash recovery free but needs an answer for work that can *never* converge, or the loop burns forever.

## Decision

- **Level-triggered:** every pass reads desired + observed, computes one convergent step, writes guarded results. Provisioning, drift repair, and deletion share this one code path.
- **Durable terminal policy:** exponential backoff with jitter under a durable retry budget; exhaustion or a permanent error class parks the resource in `phase=Failed` with the last error and an event trail. Recovery is explicit: a spec touch (new revision), or `vmctl admin retry-cleanup` for deletions (a set-once tombstone cannot be spec-touched).
- **Unschedulable is a condition, not an error:** retried on resync (the Kubernetes `Pending` pattern).
- **Operations always terminate**, even when no retry budget is being consumed: every verb carries a database-clock deadline; expiry terminalizes the Operation (`DeadlineExceeded`) *without* unsafe cleanup — desired state may keep waiting, an exposed placement stays `Unknown`, a quarantined delete keeps running — and late convergence never rewrites an immutable terminal result.
- **Agent actions carry repair incarnations:** durable retry records keyed (vm, epoch, revision, action, action_generation) with attempt-token history and server-controlled `not_before`; same-revision drift repair opens a new generation; ambiguous results (timeouts, lost responses) require re-observation before retry.
- **Bounded work:** context deadlines on every driver call; Linux subprocesses get `PR_SET_PDEATHSIG` (process groups do not help when the parent is SIGKILLed — no handler runs); a startup scavenger reclaims temp files owned by dead sessions after proving no process holds them; a bounded worker pool keeps one hung call from starving the queue.

## Consequences

- Restart-anywhere is a design property, testable by killing processes at named failpoints.
- No hidden in-memory state: attempts and backoff survive restarts, so chaos tests can assert *absence* of hot loops.

## Alternatives considered

- Saga/workflow engines: heavier, and the crash-recovery story becomes the engine's, not ours — the learning goal is the mechanism itself.
