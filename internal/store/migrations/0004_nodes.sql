-- Logical nodes, advertised by one host daemon per physical host
-- (ADR-0004). Scheduler nodes are partitions; host_id is the persistent
-- physical identity; sessions carry a generation so report ordering and
-- fencing survive daemon restarts.
CREATE TABLE nodes (
    name               text PRIMARY KEY,
    host_id            text NOT NULL,
    session_id         uuid,
    -- Bumped on every (re)registration: fences stale daemons and orders
    -- reports lexicographically by (session_generation, report_seq).
    session_generation bigint NOT NULL DEFAULT 0,
    lease_expires_at   timestamptz,
    -- Capacity quota for this logical node (validated against host
    -- allocatable at registration — no overcommit in v0.1).
    cpus               bigint NOT NULL,
    memory_bytes       bigint NOT NULL,
    disk_bytes         bigint NOT NULL,
    -- Reserved counters live here; the conditional reservation statement
    -- rechecks them atomically (D6).
    reserved_cpus      bigint NOT NULL DEFAULT 0,
    reserved_memory    bigint NOT NULL DEFAULT 0,
    reserved_disk      bigint NOT NULL DEFAULT 0,
    labels             jsonb NOT NULL DEFAULT '{}',
    created_at         timestamptz NOT NULL DEFAULT now(),
    updated_at         timestamptz NOT NULL DEFAULT now()
);

-- Host allocatable, reported by the daemon holding the host lock; the sum
-- of its logical nodes' quotas must fit inside it.
CREATE TABLE hosts (
    host_id       text PRIMARY KEY,
    cpus          bigint NOT NULL,
    memory_bytes  bigint NOT NULL,
    disk_bytes    bigint NOT NULL,
    updated_at    timestamptz NOT NULL DEFAULT now()
);
