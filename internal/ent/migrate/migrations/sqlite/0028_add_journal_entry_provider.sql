PRAGMA foreign_keys = off;
ALTER TABLE `journal_entries` ADD COLUMN `provider_program` text NULL;
ALTER TABLE `journal_entries` ADD COLUMN `provider_digest` text NULL;
PRAGMA foreign_keys = on;
