PRAGMA foreign_keys = off;
CREATE TABLE `sessions` (`id` integer NOT NULL PRIMARY KEY AUTOINCREMENT, `created_at` datetime NOT NULL, `updated_at` datetime NOT NULL, `token_hash` blob NOT NULL, `subject` text NOT NULL, `role` text NOT NULL, `scopes` json NULL, `csrf_key` blob NOT NULL, `idle_expires_at` datetime NOT NULL, `absolute_expires_at` datetime NOT NULL, `last_seen_at` datetime NOT NULL);
CREATE UNIQUE INDEX `sessions_token_hash_key` ON `sessions` (`token_hash`);
CREATE INDEX `session_token_hash` ON `sessions` (`token_hash`);
CREATE INDEX `session_absolute_expires_at` ON `sessions` (`absolute_expires_at`);
PRAGMA foreign_keys = on;
