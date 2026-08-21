PRAGMA foreign_keys = off;
ALTER TABLE `groups` ADD COLUMN `properties` json NULL;
ALTER TABLE `inventories` ADD COLUMN `properties` json NULL;
PRAGMA foreign_keys = on;
