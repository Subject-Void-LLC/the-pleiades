PRAGMA foreign_keys = off;
CREATE TABLE `activity_entries` (`id` integer NOT NULL PRIMARY KEY AUTOINCREMENT, `created_at` datetime NOT NULL, `updated_at` datetime NOT NULL, `actor` text NOT NULL, `action` text NOT NULL, `object_kind` text NOT NULL, `object_id` integer NOT NULL, `object_name` text NULL);
CREATE INDEX `activityentry_actor` ON `activity_entries` (`actor`);
CREATE INDEX `activityentry_object_kind_object_id` ON `activity_entries` (`object_kind`, `object_id`);
PRAGMA foreign_keys = on;
