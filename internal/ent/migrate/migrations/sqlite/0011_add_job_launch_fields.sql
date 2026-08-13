PRAGMA foreign_keys = off;
ALTER TABLE `jobs` ADD COLUMN `fields` json NULL;
ALTER TABLE `jobs` ADD COLUMN `extra_vars` json NULL;
PRAGMA foreign_keys = on;
