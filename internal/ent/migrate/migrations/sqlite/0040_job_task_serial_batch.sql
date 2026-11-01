PRAGMA foreign_keys = off;
ALTER TABLE `job_tasks` ADD COLUMN `batch` integer NULL;
PRAGMA foreign_keys = on;
