PRAGMA foreign_keys = off;
CREATE TABLE `local_credentials` (`id` integer NOT NULL PRIMARY KEY AUTOINCREMENT, `created_at` datetime NOT NULL, `updated_at` datetime NOT NULL, `password_hash` text NOT NULL, `failed_attempts` integer NOT NULL DEFAULT (0), `locked_until` datetime NULL, `password_changed_at` datetime NOT NULL, `must_change` bool NOT NULL DEFAULT (false), `user_local_credential` integer NOT NULL, CONSTRAINT `local_credentials_users_local_credential` FOREIGN KEY (`user_local_credential`) REFERENCES `users` (`id`) ON DELETE CASCADE);
CREATE UNIQUE INDEX `local_credentials_user_local_credential_key` ON `local_credentials` (`user_local_credential`);
PRAGMA foreign_keys = on;
