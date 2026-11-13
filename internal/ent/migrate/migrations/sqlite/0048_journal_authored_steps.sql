PRAGMA foreign_keys = off;
ALTER TABLE `journal_entries` ADD COLUMN `authored_steps` json NULL;
ALTER TABLE `journal_entries` ADD COLUMN `inverse_withheld` text NULL;
PRAGMA foreign_keys = on;
