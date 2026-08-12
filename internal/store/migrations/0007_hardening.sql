-- Hardening deferred from the PR 4-8 triage: make protocol invariants
-- unrepresentable in the schema instead of enforced by call-site courtesy.

-- Envelopes can only point at real operations; a committed envelope with a
-- dangling operation_id becomes impossible.
ALTER TABLE idempotency_envelopes
    ADD CONSTRAINT idempotency_envelopes_operation_fk
    FOREIGN KEY (operation_id) REFERENCES operations (id);

-- Vocabulary constraints: corruption fails loudly at the write.
ALTER TABLE operations
    ADD CONSTRAINT operations_state_chk CHECK (state IN
        ('PENDING','RUNNING','DONE','FAILED','SUPERSEDED','DEADLINE_EXCEEDED')),
    ADD CONSTRAINT operations_verb_chk CHECK (verb IN
        ('CREATE','UPDATE_POWER','DELETE','SNAPSHOT_CREATE','SNAPSHOT_RESTORE',
         'ADMIN_RETRY_CLEANUP','ADMIN_CLEAR_RECOVERY')),
    ADD CONSTRAINT operations_finished_chk CHECK (
        (state IN ('PENDING','RUNNING')) = (finished_at IS NULL));

ALTER TABLE vms
    ADD CONSTRAINT vms_phase_chk CHECK (phase IN
        ('PENDING','SCHEDULING','PROVISIONING','RUNNING','STOPPED',
         'UNKNOWN','FAILED','DELETING')),
    ADD CONSTRAINT vms_versions_chk CHECK (
        spec_generation >= 1 AND resource_version >= 1 AND desired_revision >= 1);

ALTER TABLE placements
    ADD CONSTRAINT placements_state_chk CHECK (state IN
        ('assigned','granted','torn_down'));

-- Terminal operations are immutable — enforced by trigger, not convention.
CREATE OR REPLACE FUNCTION reject_terminal_operation_update() RETURNS trigger AS $$
BEGIN
    IF OLD.state NOT IN ('PENDING','RUNNING') THEN
        RAISE EXCEPTION 'operation % is terminal (%) and immutable', OLD.id, OLD.state;
    END IF;
    RETURN NEW;
END $$ LANGUAGE plpgsql;

CREATE TRIGGER operations_terminal_immutable
    BEFORE UPDATE ON operations
    FOR EACH ROW EXECUTE FUNCTION reject_terminal_operation_update();

-- Tombstones are set-once and revisions monotonic — schema-enforced.
CREATE OR REPLACE FUNCTION vms_transition_guard() RETURNS trigger AS $$
BEGIN
    IF OLD.deleted_at IS NOT NULL AND NEW.deleted_at IS DISTINCT FROM OLD.deleted_at THEN
        RAISE EXCEPTION 'vm % tombstone is immutable', OLD.id;
    END IF;
    IF NEW.desired_revision < OLD.desired_revision THEN
        RAISE EXCEPTION 'vm % desired_revision must be monotonic', OLD.id;
    END IF;
    RETURN NEW;
END $$ LANGUAGE plpgsql;

CREATE TRIGGER vms_transition_guard
    BEFORE UPDATE ON vms
    FOR EACH ROW EXECUTE FUNCTION vms_transition_guard();
