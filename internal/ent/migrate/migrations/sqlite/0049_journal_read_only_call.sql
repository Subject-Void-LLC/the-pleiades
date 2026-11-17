PRAGMA foreign_keys = off;
ALTER TABLE `journal_entries` ADD COLUMN `read_only_call` bool NOT NULL DEFAULT (false);
PRAGMA foreign_keys = on;
