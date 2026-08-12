-- Durable, token-based work claims (ADR-0002, plan D4/§6.1).
--
-- FOR UPDATE SKIP LOCKED alone is a claim only while its transaction is
-- open; these columns are what serialize work across workers, lease expiry,
-- and restarts. The claim token is minted per acquisition; the committing
-- statement guards on token + input versions + an UNEXPIRED lease.
ALTER TABLE vms
    ADD COLUMN claim_token      uuid,
    ADD COLUMN claim_owner      text,
    ADD COLUMN claim_expires_at timestamptz,
    -- Durable retry state: a controller restart can neither reset backoff
    -- nor resurrect a Failed resource into a hot loop (D5).
    ADD COLUMN attempts         int          NOT NULL DEFAULT 0,
    ADD COLUMN last_error_class text         NOT NULL DEFAULT '',
    ADD COLUMN last_error       text         NOT NULL DEFAULT '',
    ADD COLUMN next_attempt_at  timestamptz  NOT NULL DEFAULT now();

-- The work queue IS this index: dirty rows whose backoff has elapsed and
-- whose claim (if any) has expired.
CREATE INDEX vms_dirty_idx ON vms (next_attempt_at)
    WHERE phase NOT IN ('FAILED');
