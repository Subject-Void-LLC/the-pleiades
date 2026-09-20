-- Reordered and backfilled by hand relative to the generator's output.
--
-- The generator emitted the `schedules` rebuild FIRST, which cannot work
-- twice over: the rebuilt table takes a NOT NULL foreign key into
-- `launchables`, a table that did not exist yet, and the copy it wrote
-- carried no value for that column, so every existing schedule would have
-- violated it. The statements below are the generator's own, in an order that
-- can be applied, with two INSERTs and one UPDATE added to carry the existing
-- rows across.
--
-- The backfill is the whole point of this migration for an existing
-- deployment: every template and every project gets the launchable row that
-- now stands for it, and every schedule is repointed from the template it
-- named at the row standing for that template. Names, organizations and
-- timestamps are copied from the target rather than invented, so a listing
-- reads the same before and after.
PRAGMA foreign_keys = off;
CREATE TABLE `launchables` (`id` integer NOT NULL PRIMARY KEY AUTOINCREMENT, `created_at` datetime NOT NULL, `updated_at` datetime NOT NULL, `type` text NOT NULL, `name` text NOT NULL, `organization_launchables` integer NOT NULL, `project_launchable` integer NULL, `template_launchable` integer NULL, CONSTRAINT `launchables_organizations_launchables` FOREIGN KEY (`organization_launchables`) REFERENCES `organizations` (`id`) ON DELETE NO ACTION, CONSTRAINT `launchables_projects_launchable` FOREIGN KEY (`project_launchable`) REFERENCES `projects` (`id`) ON DELETE CASCADE, CONSTRAINT `launchables_templates_launchable` FOREIGN KEY (`template_launchable`) REFERENCES `templates` (`id`) ON DELETE CASCADE, CONSTRAINT `launchable_exactly_one_target` CHECK (((CASE WHEN template_launchable IS NULL THEN 0 ELSE 1 END) + (CASE WHEN project_launchable IS NULL THEN 0 ELSE 1 END)) = 1));
CREATE UNIQUE INDEX `launchables_project_launchable_key` ON `launchables` (`project_launchable`);
CREATE UNIQUE INDEX `launchables_template_launchable_key` ON `launchables` (`template_launchable`);
CREATE INDEX `launchable_type_name_organization_launchables` ON `launchables` (`type`, `name`, `organization_launchables`);
INSERT INTO `launchables` (`created_at`, `updated_at`, `type`, `name`, `organization_launchables`, `template_launchable`)
  SELECT `created_at`, `updated_at`, 'job_template', `name`, `organization_templates`, `id` FROM `templates` ORDER BY `id`;
INSERT INTO `launchables` (`created_at`, `updated_at`, `type`, `name`, `organization_launchables`, `project_launchable`)
  SELECT `created_at`, `updated_at`, 'project', `name`, `organization_projects`, `id` FROM `projects` ORDER BY `id`;
CREATE TABLE `new_schedules` (`id` integer NOT NULL PRIMARY KEY AUTOINCREMENT, `created_at` datetime NOT NULL, `updated_at` datetime NOT NULL, `schedule_id` text NOT NULL, `name` text NOT NULL, `description` text NULL, `enabled` bool NOT NULL DEFAULT (true), `rrule` text NOT NULL, `exclusions` json NULL, `timezone` text NOT NULL DEFAULT ('UTC'), `dtstart` datetime NOT NULL, `dtend` datetime NULL, `next_run` datetime NULL, `last_fired` datetime NULL, `launchable_schedules` integer NOT NULL, `organization_schedules` integer NOT NULL, `schedule_saved_config` integer NULL, CONSTRAINT `schedules_launchables_schedules` FOREIGN KEY (`launchable_schedules`) REFERENCES `launchables` (`id`) ON DELETE NO ACTION, CONSTRAINT `schedules_organizations_schedules` FOREIGN KEY (`organization_schedules`) REFERENCES `organizations` (`id`) ON DELETE NO ACTION, CONSTRAINT `schedules_saved_launch_configs_saved_config` FOREIGN KEY (`schedule_saved_config`) REFERENCES `saved_launch_configs` (`id`) ON DELETE SET NULL);
INSERT INTO `new_schedules` (`id`, `created_at`, `updated_at`, `schedule_id`, `name`, `description`, `enabled`, `rrule`, `exclusions`, `timezone`, `dtstart`, `dtend`, `next_run`, `last_fired`, `organization_schedules`, `schedule_saved_config`, `launchable_schedules`)
  SELECT `s`.`id`, `s`.`created_at`, `s`.`updated_at`, `s`.`schedule_id`, `s`.`name`, `s`.`description`, `s`.`enabled`, `s`.`rrule`, `s`.`exclusions`, `s`.`timezone`, `s`.`dtstart`, `s`.`dtend`, `s`.`next_run`, `s`.`last_fired`, `s`.`organization_schedules`, `s`.`schedule_saved_config`, `l`.`id`
  FROM `schedules` `s` JOIN `launchables` `l` ON `l`.`template_launchable` = `s`.`template_schedules`;
DROP TABLE `schedules`;
ALTER TABLE `new_schedules` RENAME TO `schedules`;
CREATE UNIQUE INDEX `schedules_schedule_id_key` ON `schedules` (`schedule_id`);
CREATE UNIQUE INDEX `schedule_name_organization_schedules` ON `schedules` (`name`, `organization_schedules`);
CREATE INDEX `schedule_enabled_next_run_schedule_id` ON `schedules` (`enabled`, `next_run`, `schedule_id`);
ALTER TABLE `schedule_occurrences` ADD COLUMN `unified_job_type` text NULL;
UPDATE `schedule_occurrences` SET `unified_job_type` = 'job' WHERE `job_id` IS NOT NULL AND `job_id` <> '';
PRAGMA foreign_keys = on;
