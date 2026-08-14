PRAGMA foreign_keys = off;
ALTER TABLE `jobs` ADD COLUMN `credential_ids` json NULL;
PRAGMA foreign_keys = on;
