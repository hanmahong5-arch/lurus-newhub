-- 053_modality_and_retrieval_usage.sql
-- Idempotent, additive PG-only migration: model modality (capability routing)
-- and unified retrieval metering (_bmad-output/planning-artifacts/cycle22-tokenhub-09-parity-2026-10-10.md).
--
--   abilities.modality         VARCHAR(16) NOT NULL DEFAULT ''
--       inferred modality of the (group, model, channel) route:
--       chat | embedding | rerank | decision | image | audio; '' = unknown.
--       Rewritten whenever the channel's abilities are rebuilt.
--   models.modality            VARCHAR(16) NOT NULL DEFAULT ''
--       (entity.Model, GORM table `models`) administrator override; '' = defer to inference.
--   logs.usage_unit            VARCHAR(16) NOT NULL DEFAULT ''
--       billing unit of the row: token | search_unit | request; '' = row
--       written before this column existed (token semantics).
--   logs.usage_quantity        BIGINT NOT NULL DEFAULT 0
--       number of billing units actually charged (tokens, search units, ...).
--   logs.usage_source          VARCHAR(16) NOT NULL DEFAULT ''
--       provenance of the usage figure: upstream | estimated | unreported.
--   logs.retrieval_documents   INTEGER NOT NULL DEFAULT 0
--       rerank: documents scored; embeddings: inputs embedded.
--
-- NO index on purpose (see 051): any index on logs must be a CREATE INDEX
-- CONCURRENTLY migration.
--
-- IDEMPOTENCY / SAFETY:
--   * ADD COLUMN IF NOT EXISTS is re-run-safe.
--   * NOT NULL DEFAULT <constant> is metadata-only on PostgreSQL 11+.
--   * to_regclass guards: on a fresh database GORM AutoMigrate creates every
--     table here with the columns already present.
--   * BIGINT for usage_quantity matches the GORM tag (type:bigint) on
--     entity.Log.UsageQuantity; INTEGER for retrieval_documents matches
--     entity.Log.RetrievalDocuments (int32-safe count, GORM tag type:integer).
--   * Defaults here, in the GORM tags and in the ORM write path are identical.

DO $mig$
BEGIN
    IF to_regclass('public.abilities') IS NULL THEN
        RAISE WARNING '053_modality_and_retrieval_usage: abilities absent (AutoMigrate creates it at boot); skipping modality';
    ELSE
        ALTER TABLE abilities ADD COLUMN IF NOT EXISTS modality VARCHAR(16) NOT NULL DEFAULT '';
    END IF;

    IF to_regclass('public.models') IS NULL THEN
        RAISE WARNING '053_modality_and_retrieval_usage: models absent (AutoMigrate creates it at boot); skipping modality';
    ELSE
        ALTER TABLE models ADD COLUMN IF NOT EXISTS modality VARCHAR(16) NOT NULL DEFAULT '';
    END IF;

    IF to_regclass('public.logs') IS NULL THEN
        RAISE WARNING '053_modality_and_retrieval_usage: logs absent (AutoMigrate creates it at boot); skipping usage columns';
    ELSE
        ALTER TABLE logs ADD COLUMN IF NOT EXISTS usage_unit VARCHAR(16) NOT NULL DEFAULT '';
        ALTER TABLE logs ADD COLUMN IF NOT EXISTS usage_quantity BIGINT NOT NULL DEFAULT 0;
        ALTER TABLE logs ADD COLUMN IF NOT EXISTS usage_source VARCHAR(16) NOT NULL DEFAULT '';
        ALTER TABLE logs ADD COLUMN IF NOT EXISTS retrieval_documents INTEGER NOT NULL DEFAULT 0;
    END IF;
END
$mig$;
