ALTER TABLE "job_tasks" ADD COLUMN "result" character varying NULL, ADD COLUMN "result_reason" character varying NULL, ADD COLUMN "finished_at" timestamptz NULL;
