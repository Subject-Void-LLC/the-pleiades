PRAGMA foreign_keys = off;
ALTER TABLE `jobs` ADD COLUMN `launch_config_id` integer NULL;
PRAGMA foreign_keys = on;
