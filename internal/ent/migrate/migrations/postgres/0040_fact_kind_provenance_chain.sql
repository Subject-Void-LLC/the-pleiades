ALTER TABLE "facts" ADD COLUMN "kind" character varying NOT NULL DEFAULT 'stable', ADD COLUMN "provenance" jsonb NULL, ADD COLUMN "last_provenance" jsonb NULL, ADD COLUMN "chain_prev" character varying NULL, ADD COLUMN "chain" character varying NULL;
CREATE UNIQUE INDEX "fact_chain_prev_device_facts" ON "facts" ("chain_prev", "device_facts");
