PRAGMA foreign_keys = off;
CREATE TABLE `jobs` (`id` integer NOT NULL PRIMARY KEY AUTOINCREMENT, `created_at` datetime NOT NULL, `updated_at` datetime NOT NULL, `job_id` text NOT NULL, `runbook_id` text NOT NULL, `group_name` text NOT NULL, `actor` text NOT NULL, `state` text NOT NULL DEFAULT ('pending'), `dispatched_count` integer NOT NULL DEFAULT (0), `skipped_count` integer NOT NULL DEFAULT (0), `failed_count` integer NOT NULL DEFAULT (0), `failure_reason` text NULL, `fence` integer NOT NULL DEFAULT (0));
CREATE UNIQUE INDEX `jobs_job_id_key` ON `jobs` (`job_id`);
CREATE TABLE `job_tasks` (`id` integer NOT NULL PRIMARY KEY AUTOINCREMENT, `created_at` datetime NOT NULL, `updated_at` datetime NOT NULL, `device_id` text NOT NULL, `device_name` text NOT NULL, `outcome` text NOT NULL, `reason` text NULL, `job_tasks` integer NOT NULL, CONSTRAINT `job_tasks_jobs_tasks` FOREIGN KEY (`job_tasks`) REFERENCES `jobs` (`id`) ON DELETE NO ACTION);
CREATE INDEX `jobtask_outcome_job_tasks` ON `job_tasks` (`outcome`, `job_tasks`);
PRAGMA foreign_keys = on;
