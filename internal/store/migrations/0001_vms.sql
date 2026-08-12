-- The VM resource row: the single source of truth the API and reconciler
-- share. Claim/retry columns, placements, operations, and envelopes land
-- with the PRs that implement those protocols — the schema grows as the
-- concepts are taught.
CREATE TABLE vms (
    id               uuid PRIMARY KEY,
    name             text NOT NULL UNIQUE,
    -- protojson-encoded vmc.v1.VmSpec / VmStatus.
    spec             jsonb NOT NULL,
    status           jsonb NOT NULL DEFAULT '{}',
    phase            text  NOT NULL DEFAULT 'PENDING',
    -- User-intent version (power is the only v0.1-mutable field).
    spec_generation  bigint NOT NULL DEFAULT 1,
    -- Optimistic lock: bumped by EVERY writer; all mutations CAS on it.
    resource_version bigint NOT NULL DEFAULT 1,
    -- Monotonic desired revision: spec generations ⊕ deletion tombstone.
    desired_revision bigint NOT NULL DEFAULT 1,
    -- Scheduling state (owned by the scheduler PR onward).
    node_name        text,
    placement_epoch  bigint NOT NULL DEFAULT 0,
    -- Tombstone: deletion is reconciliation; the row outlives the substrate.
    deleted_at       timestamptz,
    created_at       timestamptz NOT NULL DEFAULT now(),
    updated_at       timestamptz NOT NULL DEFAULT now()
);
