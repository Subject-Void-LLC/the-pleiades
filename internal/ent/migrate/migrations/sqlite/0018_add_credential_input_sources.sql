PRAGMA foreign_keys = off;
CREATE TABLE `credential_input_sources` (`id` integer NOT NULL PRIMARY KEY AUTOINCREMENT, `created_at` datetime NOT NULL, `updated_at` datetime NOT NULL, `input_id` text NOT NULL, `metadata` json NULL, `credential_input_sources` integer NOT NULL, `credential_sourced_by` integer NOT NULL, CONSTRAINT `credential_input_sources_credentials_input_sources` FOREIGN KEY (`credential_input_sources`) REFERENCES `credentials` (`id`) ON DELETE CASCADE, CONSTRAINT `credential_input_sources_credentials_sourced_by` FOREIGN KEY (`credential_sourced_by`) REFERENCES `credentials` (`id`) ON DELETE CASCADE);
CREATE UNIQUE INDEX `credentialinputsource_input_id_credential_input_sources` ON `credential_input_sources` (`input_id`, `credential_input_sources`);
PRAGMA foreign_keys = on;
