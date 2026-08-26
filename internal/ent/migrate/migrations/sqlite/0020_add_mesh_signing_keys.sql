PRAGMA foreign_keys = off;
CREATE TABLE `mesh_signing_keys` (`id` integer NOT NULL PRIMARY KEY AUTOINCREMENT, `created_at` datetime NOT NULL, `updated_at` datetime NOT NULL, `key_id` text NOT NULL, `account_subject` text NOT NULL, `public_key` text NOT NULL, `seed` text NOT NULL, `active` bool NOT NULL DEFAULT (false), `secret_binding` text NOT NULL);
CREATE UNIQUE INDEX `mesh_signing_keys_key_id_key` ON `mesh_signing_keys` (`key_id`);
CREATE INDEX `meshsigningkey_account_subject` ON `mesh_signing_keys` (`account_subject`);
PRAGMA foreign_keys = on;
