-- Ordered evidence (plan §6.5): reports are accepted only in lexicographic
-- (session_generation, report_seq) order per VM — a delayed Running report
-- cannot overwrite newer ShutOff drift evidence, and a restarted daemon's
-- fresh session (higher generation, seq 1) is not rejected behind the old
-- session's high sequence.
ALTER TABLE vms
    ADD COLUMN observed_state       text   NOT NULL DEFAULT '',
    ADD COLUMN applied_revision     bigint NOT NULL DEFAULT 0,
    ADD COLUMN report_session_gen   bigint NOT NULL DEFAULT 0,
    ADD COLUMN report_seq           bigint NOT NULL DEFAULT 0,
    ADD COLUMN observed_detail      text   NOT NULL DEFAULT '';

-- Durable per-action retry pacing (plan D5): the server owns not_before;
-- the daemon executes only when due; results are accepted idempotently by
-- attempt token. Fake-tier scope: one logical action per (vm, epoch,
-- revision) — per-action granularity arrives with the real drivers.
CREATE TABLE action_retries (
    vm_id         uuid   NOT NULL,
    epoch         bigint NOT NULL,
    revision      bigint NOT NULL,
    generation    bigint NOT NULL DEFAULT 1,
    attempts      int    NOT NULL DEFAULT 0,
    not_before    timestamptz NOT NULL DEFAULT now(),
    attempt_token uuid   NOT NULL DEFAULT gen_random_uuid(),
    PRIMARY KEY (vm_id, epoch, revision, generation)
);
