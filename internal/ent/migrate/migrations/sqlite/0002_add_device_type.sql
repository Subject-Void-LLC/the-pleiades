PRAGMA foreign_keys = off;
ALTER TABLE `devices` ADD COLUMN `type` text NOT NULL;
PRAGMA foreign_keys = on;
