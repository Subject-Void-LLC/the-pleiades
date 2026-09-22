PRAGMA foreign_keys = off;
CREATE TABLE `controller_instances` (`id` integer NOT NULL PRIMARY KEY AUTOINCREMENT, `created_at` datetime NOT NULL, `updated_at` datetime NOT NULL, `instance_id` text NOT NULL, `version` text NOT NULL, `migration_head` text NOT NULL, `host` text NOT NULL DEFAULT (''), `started_unix` integer NOT NULL, `last_seen_unix` integer NOT NULL);
CREATE UNIQUE INDEX `controller_instances_instance_id_key` ON `controller_instances` (`instance_id`);
CREATE INDEX `controllerinstance_last_seen_unix` ON `controller_instances` (`last_seen_unix`);
PRAGMA foreign_keys = on;
