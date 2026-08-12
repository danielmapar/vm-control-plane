-- Operations: the always-terminating result of every mutating RPC (D3).
-- NOT foreign-keyed to resource tables: a Delete operation must remain
-- queryable after its resource row is gone.
CREATE TABLE operations (
    id               uuid PRIMARY KEY,
    resource_type    text NOT NULL,
    resource_id      uuid NOT NULL,
    resource_name    text NOT NULL,
    verb             text NOT NULL,
    -- The desired revision this operation realizes; completion requires
    -- evidence of exactly this revision.
    target_revision  bigint NOT NULL,
    state            text NOT NULL DEFAULT 'PENDING',
    error            text NOT NULL DEFAULT '',
    -- Database-clock deadline: expiry terminalizes as DEADLINE_EXCEEDED
    -- even when no retry budget is being consumed (Unschedulable forever,
    -- Unknown after grant, unreachable delete).
    deadline         timestamptz NOT NULL,
    created_at       timestamptz NOT NULL DEFAULT now(),
    finished_at      timestamptz
);

CREATE INDEX operations_resource_idx ON operations (resource_type, resource_id, created_at DESC);
-- The reconciler scans for expired non-terminal operations.
CREATE INDEX operations_deadline_idx ON operations (deadline)
    WHERE state IN ('PENDING', 'RUNNING');

-- Idempotency envelopes: the serialization point for EVERY mutating verb
-- (user and admin). Inserted (or conflicted on) BEFORE any precondition
-- processing. No TTL in v0.1: expiring keys reopens the replay window.
CREATE TABLE idempotency_envelopes (
    key              uuid PRIMARY KEY,
    method           text NOT NULL,
    api_version      text NOT NULL,
    resource_type    text NOT NULL,
    resource_name    text NOT NULL,
    -- sha256 of the canonical (deterministic) serialization of the request
    -- minus the key itself. Same key + same hash -> return original
    -- operation; same key + different hash -> FAILED_PRECONDITION.
    request_hash     bytea NOT NULL,
    operation_id     uuid,
    created_at       timestamptz NOT NULL DEFAULT now()
);
