PRAGMA foreign_keys = off;
ALTER TABLE `journal_entries` ADD COLUMN `authored_steps` json NULL;
PRAGMA foreign_keys = on;
