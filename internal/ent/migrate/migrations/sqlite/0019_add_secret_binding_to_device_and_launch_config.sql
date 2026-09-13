PRAGMA foreign_keys = off;
ALTER TABLE `devices` ADD COLUMN `secret_binding` text NULL;
ALTER TABLE `saved_launch_configs` ADD COLUMN `secret_binding` text NULL;
PRAGMA foreign_keys = on;
