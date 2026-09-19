PRAGMA foreign_keys = off;
PRAGMA foreign_keys = off;
CREATE TABLE `new_job_tasks` (`id` integer NOT NULL PRIMARY KEY AUTOINCREMENT, `created_at` datetime NOT NULL, `updated_at` datetime NOT NULL, `device_id` text NOT NULL, `device_name` text NOT NULL, `outcome` text NOT NULL, `reason` text NULL, `result` text NULL, `result_reason` text NULL, `finished_at` datetime NULL, `unchecked` integer NOT NULL DEFAULT (0), `job_tasks` integer NOT NULL, CONSTRAINT `job_tasks_jobs_tasks` FOREIGN KEY (`job_tasks`) REFERENCES `jobs` (`id`) ON DELETE NO ACTION);
INSERT INTO `new_job_tasks` (`id`, `created_at`, `updated_at`, `device_id`, `device_name`, `outcome`, `reason`, `result`, `result_reason`, `finished_at`, `job_tasks`) SELECT `id`, `created_at`, `updated_at`, `device_id`, `device_name`, `outcome`, `reason`, `result`, `result_reason`, `finished_at`, `job_tasks` FROM `job_tasks`;
DROP TABLE `job_tasks`;
ALTER TABLE `new_job_tasks` RENAME TO `job_tasks`;
CREATE INDEX `jobtask_outcome_job_tasks` ON `job_tasks` (`outcome`, `job_tasks`);
PRAGMA foreign_keys = on;
PRAGMA foreign_keys = on;
