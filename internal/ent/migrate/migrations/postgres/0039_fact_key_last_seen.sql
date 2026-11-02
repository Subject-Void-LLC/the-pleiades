ALTER TABLE "facts" ADD COLUMN "key" character varying NOT NULL DEFAULT '', ADD COLUMN "last_seen_at" timestamptz NULL;
CREATE INDEX "fact_key_device_facts" ON "facts" ("key", "device_facts");
CREATE INDEX "fact_device_facts" ON "facts" ("device_facts");
