-- Expand-only: four claim columns on job_tasks (Phase 41b). Written as ALTER
-- TABLE rather than a table rebuild, so the table's sqlite_sequence survives
-- and a deleted row's id is never handed out again.
ALTER TABLE `job_tasks` ADD COLUMN `claim_attempt` integer NOT NULL DEFAULT (0);
ALTER TABLE `job_tasks` ADD COLUMN `claim_runner` text NOT NULL DEFAULT ('');
ALTER TABLE `job_tasks` ADD COLUMN `claim_order_source` text NOT NULL DEFAULT ('');
ALTER TABLE `job_tasks` ADD COLUMN `claim_order_token` integer NOT NULL DEFAULT (0);
