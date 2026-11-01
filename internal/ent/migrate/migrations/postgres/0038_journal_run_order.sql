ALTER TABLE "journal_entries" ADD COLUMN "run_tick" bigint NOT NULL DEFAULT 0, ADD COLUMN "lease_revision" bigint NOT NULL DEFAULT 0;
CREATE INDEX "journalentry_device_id_lease_revision" ON "journal_entries" ("device_id", "lease_revision");
