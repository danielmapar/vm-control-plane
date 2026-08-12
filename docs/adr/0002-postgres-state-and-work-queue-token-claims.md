# ADR-0002: PostgreSQL for state *and* work queue, with token-based durable claims

**Status:** Accepted · **Date:** 2026-08-12

## Context

The reconciler needs a crash-safe work queue. Adding Kafka/Redis introduces new failure modes and operational surface for a system whose truth already lives in PostgreSQL. But the naive PG-queue pattern — `SELECT … FOR UPDATE SKIP LOCKED`, do the work, commit — is only a claim *while the transaction is open*: commit early and another worker can duplicate external side effects; hold the lock and you pin a pool connection across network calls.

## Decision

The queue is the set of dirty rows (desired revision unrealized, or transitional phase). Claims are **durable and token-based**:

- Acquisition is one atomic `UPDATE … RETURNING` that mints an immutable **claim token** and sets `claim_expires_at` (lease). SKIP LOCKED handles contention; the lease handles time.
- Renewal and completion are guarded **by the token** — not by worker name (not unique per acquisition) and not by the global `resource_version` (legitimately bumped by observation traffic; using it would spuriously invalidate completions or, worse, re-authorize stale computations).
- **The commit guard is lease-aware:** the statement that commits a transition requires token + input versions **+ `claim_expires_at > clock_timestamp()`**. A token does not outlive its lease — an expired worker cannot commit even before takeover replaces it; takeover serializes on the claim row. Every durable side effect of a transition (including scheduler reservations and placements) commits inside that one guarded transaction.
- The claim records the **(desired revision, observed version)** the computation read; the completing write CASes on those, so a stale worker's late completion no-ops.
- Retry state (`attempts`, `last_error_class`, `next_attempt_at`) is durable: restarts cannot reset backoff or resurrect a `Failed` resource into a hot loop.

## Consequences

- Overlap after lease expiry is *allowed and safe*: external effects are idempotent + fenced (ADR-0004), and the loser's completion loses the CAS.
- Crash recovery is a scan, not a replay: the queue *is* the dirty rows.
- Tests required (and written): renewal-vs-completion, takeover under the same worker identity, completion under continuous observation traffic, expired-lease late completion.

## Alternatives considered

- Message broker: real-world common, but adds a second source of truth and dual-write hazards at this scope.
- Long-held row locks: pins connections, caps concurrency at pool size, and still doesn't cover external side effects.
