PRAGMA foreign_keys = off;
CREATE TABLE `sync_runs` (`id` integer NOT NULL PRIMARY KEY AUTOINCREMENT, `created_at` datetime NOT NULL, `updated_at` datetime NOT NULL, `status` text NOT NULL, `revision` text NOT NULL DEFAULT (''), `error` text NOT NULL DEFAULT (''), `started_at` datetime NOT NULL, `finished_at` datetime NOT NULL, `project_sync_runs` integer NOT NULL, CONSTRAINT `sync_runs_projects_sync_runs` FOREIGN KEY (`project_sync_runs`) REFERENCES `projects` (`id`) ON DELETE NO ACTION);
CREATE INDEX `syncrun_started_at_project_sync_runs` ON `sync_runs` (`started_at`, `project_sync_runs`);
PRAGMA foreign_keys = on;
