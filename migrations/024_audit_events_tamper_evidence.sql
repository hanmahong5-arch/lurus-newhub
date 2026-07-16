-- 024_audit_events_tamper_evidence.sql
-- Idempotent PG-only tamper evidence for the governance audit trail:
--   1. audit_events gains prev_hash / row_hash (per-tenant SHA-256 hash chain,
--      computed application-side in entity.AuditEvent.BeforeCreate).
--   2. audit_chain_heads — one row per tenant holding the newest chained
--      row_hash; writers lock it (SELECT ... FOR UPDATE) inside the insert
--      transaction so concurrent audit writes cannot fork the chain.
--   3. A BEFORE UPDATE/DELETE row trigger + BEFORE TRUNCATE statement trigger
--      make the table append-only at the DATABASE level (fences the table
--      owner too — a REVOKE cannot; mirrors platform migration 081).
--
-- WHY: audit rows were plain GORM rows — any code path or leaked psql session
-- could UPDATE/DELETE them and silently rewrite the forensic trail. The only
-- hash previously near this data (governance.ComputeFingerprint) is explicitly
-- "NOT a security hash". Chain verification is exposed at
-- GET /api/v2/admin/audit/chain-verify (root-only).
--
-- HASH-CHAIN FIELD CONTRACT (must match entity.ComputeAuditRowHash):
--   COVERED : tenant_id, timestamp, actor_type, actor_id, action, resource,
--             resource_id, request_id, retention_until, prev_hash.
--   EXCLUDED: ip, details — the PIPL §47 erasure path
--             (repo.ScrubAuditEventsBatch) lawfully rewrites them in place;
--             excluding them keeps redaction from breaking the chain.
--             id — assigned during INSERT, after the hash is final.
--
-- ESCAPE HATCHES (each maps to one verified production writer):
--   * UPDATE  → allowed ONLY inside a tx that set app.audit_redaction='on'
--               (transaction-local set_config, platform-081 pattern), and even
--               then ONLY ip/details may change — every hash-covered column is
--               compared OLD vs NEW and rejected on drift. Sole caller:
--               repo.ScrubAuditEventsBatch (PIPL §47 audit scrub).
--   * DELETE  → allowed ONLY for rows whose retention deadline has genuinely
--               passed (retention_until > 0 AND <= now). Sole caller:
--               repo.DeleteExpiredAuditEvents via the audit_cleanup lifecycle
--               task. NOTE: this deliberately deviates from "reject all
--               DELETEs" — a blanket block would break the shipped retention
--               sweep (internal/lifecycle/audit_cleanup.go) and grow the table
--               forever. No GUC is needed: UPDATE is blocked, so an attacker
--               cannot shorten retention_until to sneak a row into the
--               deletable window. Mid-chain retention pruning severs prev_hash
--               linkage; chain-verify reports that as link_breaks (separate
--               from content hash_breaks) for operator correlation.
--   * TRUNCATE → unconditionally blocked (would wipe the trail in one shot).
--
-- EXECUTION CONTRACT (internal/pkg/migration/runner.go): this body runs in ONE
-- transaction; the Runner appends the schema_migrations record afterwards and
-- does NOT use ON CONFLICT on that record, so the body MUST be fully idempotent.
--
-- IDEMPOTENCY / SAFETY:
--   * CREATE TABLE IF NOT EXISTS / ADD COLUMN IF NOT EXISTS /
--     CREATE OR REPLACE FUNCTION / DROP TRIGGER IF EXISTS → re-runs are no-ops.
--   * audit_events itself is created by GORM AutoMigrate, which runs BEFORE the
--     Runner on the boot lease winner; on runner-only databases (tests) where
--     the table is absent, the column/trigger section is skipped with a WARNING
--     (to_regclass guard, same convention as 022).
--   * Existing rows keep prev_hash/row_hash = '' (legacy segment — the chain
--     starts at enablement; verification reports legacy rows, never fails them).

-- §1 Per-tenant chain heads (standalone; also the app's serialization point).
CREATE TABLE IF NOT EXISTS audit_chain_heads (
    tenant_id  VARCHAR(36) PRIMARY KEY,
    last_hash  VARCHAR(64) NOT NULL DEFAULT '',
    updated_at BIGINT      NOT NULL DEFAULT 0
);

COMMENT ON TABLE audit_chain_heads IS
    '024: per-tenant audit hash-chain head; locked FOR UPDATE inside each audit insert tx to serialize the chain.';

-- §2 Append-only guard function (row level, UPDATE/DELETE).
CREATE OR REPLACE FUNCTION audit_events_tamper_guard()
RETURNS trigger
LANGUAGE plpgsql
AS $$
BEGIN
    IF TG_OP = 'UPDATE' THEN
        -- Sanctioned PIPL §47 redaction: tx-scoped GUC, PII columns only.
        IF COALESCE(NULLIF(current_setting('app.audit_redaction', true), ''), 'off') = 'on' THEN
            IF NEW.id              IS DISTINCT FROM OLD.id
               OR NEW.tenant_id       IS DISTINCT FROM OLD.tenant_id
               OR NEW."timestamp"     IS DISTINCT FROM OLD."timestamp"
               OR NEW.actor_type      IS DISTINCT FROM OLD.actor_type
               OR NEW.actor_id        IS DISTINCT FROM OLD.actor_id
               OR NEW.action          IS DISTINCT FROM OLD.action
               OR NEW.resource        IS DISTINCT FROM OLD.resource
               OR NEW.resource_id     IS DISTINCT FROM OLD.resource_id
               OR NEW.request_id      IS DISTINCT FROM OLD.request_id
               OR NEW.retention_until IS DISTINCT FROM OLD.retention_until
               OR NEW.prev_hash       IS DISTINCT FROM OLD.prev_hash
               OR NEW.row_hash        IS DISTINCT FROM OLD.row_hash THEN
                RAISE EXCEPTION 'audit_events redaction may only change ip/details; hash-chain columns are immutable'
                    USING ERRCODE = 'check_violation';
            END IF;
            RETURN NEW;
        END IF;
        RAISE EXCEPTION 'audit_events is append-only: UPDATE blocked (only the PIPL redaction path may update, via tx-local app.audit_redaction=on)'
            USING ERRCODE = 'check_violation';
    ELSIF TG_OP = 'DELETE' THEN
        -- Retention carve-out: only genuinely expired rows are prunable
        -- (audit_cleanup lifecycle). UPDATE is blocked above, so
        -- retention_until cannot be forged to widen this window.
        IF OLD.retention_until > 0
           AND OLD.retention_until <= floor(extract(epoch FROM now()))::bigint THEN
            RETURN OLD;
        END IF;
        RAISE EXCEPTION 'audit_events is append-only: DELETE blocked (only retention-expired rows may be pruned by audit_cleanup)'
            USING ERRCODE = 'check_violation';
    END IF;
    RETURN NULL; -- unreachable: trigger fires only on UPDATE/DELETE
END;
$$;

COMMENT ON FUNCTION audit_events_tamper_guard() IS
    '024: append-only guard — UPDATE only under tx-local app.audit_redaction=on and only ip/details; DELETE only for retention-expired rows.';

-- §3 TRUNCATE guard (row triggers do not fire on TRUNCATE).
CREATE OR REPLACE FUNCTION audit_events_block_truncate()
RETURNS trigger
LANGUAGE plpgsql
AS $$
BEGIN
    RAISE EXCEPTION 'audit_events is append-only: TRUNCATE blocked'
        USING ERRCODE = 'check_violation';
END;
$$;

-- §4 Columns + index + triggers on audit_events (guarded: AutoMigrate creates
-- the table before the Runner in production boot; runner-only DBs skip).
DO $mig$
BEGIN
    IF to_regclass('public.audit_events') IS NULL THEN
        RAISE WARNING '024_audit_events_tamper_evidence: audit_events absent (AutoMigrate creates it at boot); skipping column/trigger section';
        RETURN;
    END IF;

    EXECUTE 'ALTER TABLE audit_events ADD COLUMN IF NOT EXISTS prev_hash VARCHAR(64) NOT NULL DEFAULT ''''';
    EXECUTE 'ALTER TABLE audit_events ADD COLUMN IF NOT EXISTS row_hash  VARCHAR(64) NOT NULL DEFAULT ''''';

    -- chain-verify scans ORDER BY id within a tenant.
    EXECUTE 'CREATE INDEX IF NOT EXISTS idx_audit_events_tenant_id_id ON audit_events (tenant_id, id)';

    EXECUTE 'DROP TRIGGER IF EXISTS audit_events_tamper_guard ON audit_events';
    EXECUTE 'CREATE TRIGGER audit_events_tamper_guard
                 BEFORE UPDATE OR DELETE ON audit_events
                 FOR EACH ROW
                 EXECUTE FUNCTION audit_events_tamper_guard()';

    EXECUTE 'DROP TRIGGER IF EXISTS audit_events_block_truncate ON audit_events';
    EXECUTE 'CREATE TRIGGER audit_events_block_truncate
                 BEFORE TRUNCATE ON audit_events
                 FOR EACH STATEMENT
                 EXECUTE FUNCTION audit_events_block_truncate()';
END
$mig$;
