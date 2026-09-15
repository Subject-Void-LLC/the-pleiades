-- Reordered by hand relative to the generator's output, which emitted
-- the templates rebuild before the table it takes a foreign key into.
-- SQLite has no ADD CONSTRAINT, so adding templates.project_templates
-- means rebuilding the whole table, and the rebuilt definition
-- references `projects`. Applied in the generated order it fails with
-- "no such table: main.projects". The postgres migration for the same
-- schema change is already in this order because ALTER TABLE ... ADD
-- CONSTRAINT needs no rebuild there.
PRAGMA foreign_keys = off;
CREATE TABLE `projects` (`id` integer NOT NULL PRIMARY KEY AUTOINCREMENT, `created_at` datetime NOT NULL, `updated_at` datetime NOT NULL, `name` text NOT NULL, `description` text NOT NULL DEFAULT (''), `scm_type` text NOT NULL DEFAULT ('git'), `scm_url` text NOT NULL DEFAULT (''), `scm_branch` text NOT NULL DEFAULT (''), `local_path` text NOT NULL DEFAULT (''), `revision` text NOT NULL DEFAULT (''), `sync_status` text NOT NULL DEFAULT ('never'), `sync_error` text NOT NULL DEFAULT (''), `last_synced_at` datetime NULL, `credential_projects` integer NULL, `organization_projects` integer NOT NULL, CONSTRAINT `projects_credentials_projects` FOREIGN KEY (`credential_projects`) REFERENCES `credentials` (`id`) ON DELETE SET NULL, CONSTRAINT `projects_organizations_projects` FOREIGN KEY (`organization_projects`) REFERENCES `organizations` (`id`) ON DELETE NO ACTION);
CREATE UNIQUE INDEX `project_name_organization_projects` ON `projects` (`name`, `organization_projects`);
CREATE TABLE `new_templates` (`id` integer NOT NULL PRIMARY KEY AUTOINCREMENT, `created_at` datetime NOT NULL, `updated_at` datetime NOT NULL, `name` text NOT NULL, `description` text NULL, `kind` text NOT NULL, `definition` text NOT NULL, `defaults` json NULL, `prompts` json NULL, `required_caps` json NULL, `survey_enabled` bool NOT NULL DEFAULT (false), `allow_simultaneous` bool NOT NULL DEFAULT (false), `inventory_templates` integer NOT NULL, `organization_templates` integer NOT NULL, `project_templates` integer NULL, CONSTRAINT `templates_inventories_templates` FOREIGN KEY (`inventory_templates`) REFERENCES `inventories` (`id`) ON DELETE NO ACTION, CONSTRAINT `templates_organizations_templates` FOREIGN KEY (`organization_templates`) REFERENCES `organizations` (`id`) ON DELETE NO ACTION, CONSTRAINT `templates_projects_templates` FOREIGN KEY (`project_templates`) REFERENCES `projects` (`id`) ON DELETE SET NULL);
INSERT INTO `new_templates` (`id`, `created_at`, `updated_at`, `name`, `description`, `kind`, `definition`, `defaults`, `prompts`, `required_caps`, `survey_enabled`, `allow_simultaneous`, `inventory_templates`, `organization_templates`) SELECT `id`, `created_at`, `updated_at`, `name`, `description`, `kind`, `definition`, `defaults`, `prompts`, `required_caps`, `survey_enabled`, `allow_simultaneous`, `inventory_templates`, `organization_templates` FROM `templates`;
DROP TABLE `templates`;
ALTER TABLE `new_templates` RENAME TO `templates`;
CREATE UNIQUE INDEX `template_name_organization_templates` ON `templates` (`name`, `organization_templates`);
CREATE INDEX `template_kind_definition` ON `templates` (`kind`, `definition`);
PRAGMA foreign_keys = on;
