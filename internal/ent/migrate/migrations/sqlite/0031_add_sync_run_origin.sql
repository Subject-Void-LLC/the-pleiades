PRAGMA foreign_keys = off;
PRAGMA foreign_keys = off;
CREATE TABLE `new_sync_runs` (`id` integer NOT NULL PRIMARY KEY AUTOINCREMENT, `created_at` datetime NOT NULL, `updated_at` datetime NOT NULL, `status` text NOT NULL, `actor` text NULL, `revision` text NOT NULL DEFAULT (''), `error` text NOT NULL DEFAULT (''), `started_at` datetime NOT NULL, `finished_at` datetime NULL, `project_sync_runs` integer NOT NULL, CONSTRAINT `sync_runs_projects_sync_runs` FOREIGN KEY (`project_sync_runs`) REFERENCES `projects` (`id`) ON DELETE CASCADE);
INSERT INTO `new_sync_runs` (`id`, `created_at`, `updated_at`, `status`, `revision`, `error`, `started_at`, `finished_at`, `project_sync_runs`) SELECT `id`, `created_at`, `updated_at`, `status`, `revision`, `error`, `started_at`, `finished_at`, `project_sync_runs` FROM `sync_runs`;
DROP TABLE `sync_runs`;
ALTER TABLE `new_sync_runs` RENAME TO `sync_runs`;
CREATE INDEX `syncrun_started_at_project_sync_runs` ON `sync_runs` (`started_at`, `project_sync_runs`);
PRAGMA foreign_keys = on;
PRAGMA foreign_keys = on;
