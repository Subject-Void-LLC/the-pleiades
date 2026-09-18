PRAGMA foreign_keys = off;
CREATE TABLE `encryption_keys` (`id` integer NOT NULL PRIMARY KEY AUTOINCREMENT, `created_at` datetime NOT NULL, `updated_at` datetime NOT NULL, `fingerprint` text NOT NULL, `version` text NOT NULL, `origin` text NOT NULL, `possession` text NOT NULL);
CREATE UNIQUE INDEX `encryption_keys_fingerprint_key` ON `encryption_keys` (`fingerprint`);
PRAGMA foreign_keys = on;
