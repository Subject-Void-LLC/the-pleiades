PRAGMA foreign_keys = off;
ALTER TABLE `sync_runs` ADD COLUMN `owner_instance` text NULL;
PRAGMA foreign_keys = on;
