PRAGMA foreign_keys = off;
PRAGMA foreign_keys = off;
CREATE TABLE `new_facts` (`id` integer NOT NULL PRIMARY KEY AUTOINCREMENT, `created_at` datetime NOT NULL, `updated_at` datetime NOT NULL, `key` text NOT NULL DEFAULT (''), `payload` json NOT NULL, `hash` text NOT NULL, `last_seen_at` datetime NULL, `device_facts` integer NOT NULL, CONSTRAINT `facts_devices_facts` FOREIGN KEY (`device_facts`) REFERENCES `devices` (`id`) ON DELETE NO ACTION);
INSERT INTO `new_facts` (`id`, `created_at`, `updated_at`, `payload`, `hash`, `device_facts`) SELECT `id`, `created_at`, `updated_at`, `payload`, `hash`, `device_facts` FROM `facts`;
DROP TABLE `facts`;
ALTER TABLE `new_facts` RENAME TO `facts`;
CREATE INDEX `fact_key_device_facts` ON `facts` (`key`, `device_facts`);
PRAGMA foreign_keys = on;
PRAGMA foreign_keys = on;
