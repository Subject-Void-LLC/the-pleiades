ALTER TABLE "jobs" ADD COLUMN "canceled_at" timestamptz NULL, ADD COLUMN "canceled_by" character varying NULL;
