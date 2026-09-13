ALTER TABLE "devices" ADD COLUMN "secret_binding" character varying NULL;
ALTER TABLE "saved_launch_configs" ADD COLUMN "secret_binding" character varying NULL;
