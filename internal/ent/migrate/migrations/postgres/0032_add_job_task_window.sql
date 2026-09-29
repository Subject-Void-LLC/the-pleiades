ALTER TABLE "job_tasks" ADD COLUMN "waiting" boolean NOT NULL DEFAULT false, ADD COLUMN "slot" bigint NULL;
CREATE UNIQUE INDEX "jobtask_slot_job_tasks" ON "job_tasks" ("slot", "job_tasks");
