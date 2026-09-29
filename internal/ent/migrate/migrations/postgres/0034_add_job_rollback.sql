ALTER TABLE "jobs" ADD COLUMN "rollback_of" character varying NULL, ADD COLUMN "rollback" jsonb NULL;
CREATE INDEX "job_rollback_of" ON "jobs" ("rollback_of");
