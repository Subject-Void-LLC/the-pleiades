PRAGMA foreign_keys = off;
ALTER TABLE `jobs` ADD COLUMN `canceled_at` datetime NULL;
ALTER TABLE `jobs` ADD COLUMN `canceled_by` text NULL;
PRAGMA foreign_keys = on;
