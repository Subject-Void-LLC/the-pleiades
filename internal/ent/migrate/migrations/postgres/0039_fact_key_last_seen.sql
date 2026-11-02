ALTER TABLE "facts" ADD COLUMN "key" character varying NOT NULL, ADD COLUMN "last_seen_at" timestamptz NOT NULL;
CREATE INDEX "fact_key_device_facts" ON "facts" ("key", "device_facts");
