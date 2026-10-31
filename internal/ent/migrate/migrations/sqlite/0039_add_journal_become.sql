PRAGMA foreign_keys = off;
ALTER TABLE `journal_entries` ADD COLUMN `become` text NULL;
PRAGMA foreign_keys = on;
