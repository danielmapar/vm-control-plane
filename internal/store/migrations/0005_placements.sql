-- Placements: the grant/fencing ledger (plan §6). One row per (vm, epoch);
-- the partial unique index makes double placement UNREPRESENTABLE — the
-- schema, not code discipline, forbids two active placements.
CREATE TABLE placements (
    vm_id      uuid   NOT NULL,
    epoch      bigint NOT NULL,
    node_name  text   NOT NULL,
    host_id    text   NOT NULL,
    -- assigned -> granted (exposure! substrate actions allowed) -> torn_down
    state      text   NOT NULL DEFAULT 'assigned',
    granted_at timestamptz,
    created_at timestamptz NOT NULL DEFAULT now(),
    PRIMARY KEY (vm_id, epoch)
);

CREATE UNIQUE INDEX placements_one_active_idx
    ON placements (vm_id) WHERE state <> 'torn_down';

-- Reservations: unique per (vm, epoch); release is an idempotent
-- DELETE ... RETURNING whose result drives the counter decrement — a
-- double release is harmless by construction (D6).
CREATE TABLE reservations (
    vm_id        uuid   NOT NULL,
    epoch        bigint NOT NULL,
    node_name    text   NOT NULL,
    cpus         bigint NOT NULL,
    memory_bytes bigint NOT NULL,
    disk_bytes   bigint NOT NULL,
    created_at   timestamptz NOT NULL DEFAULT now(),
    PRIMARY KEY (vm_id, epoch)
);
