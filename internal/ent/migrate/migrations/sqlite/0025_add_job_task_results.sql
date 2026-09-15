PRAGMA foreign_keys = off;
ALTER TABLE `job_tasks` ADD COLUMN `result` text NULL;
ALTER TABLE `job_tasks` ADD COLUMN `result_reason` text NULL;
ALTER TABLE `job_tasks` ADD COLUMN `finished_at` datetime NULL;
PRAGMA foreign_keys = on;
