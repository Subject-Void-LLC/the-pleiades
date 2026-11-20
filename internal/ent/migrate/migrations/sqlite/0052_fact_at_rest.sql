-- Facts at rest: a stored fact names nothing in plaintext. Expand-only.
-- A row is found by key_token and says which keys made it in keyed_by, and
-- payload holds the key and the value sealed. Every fact stored before this
-- is plaintext and carries a plain hash, which no key can turn into a token
-- or a keyed hash, so those rows are deleted with their observations; the
-- next gather stores each device's facts again, sealed. The deleted rows'
-- bytes are then removed from the file by a VACUUM this migration cannot run
-- itself (internal/ent/migrate's sqliteAfterCommit).
-- Written as ALTER TABLE, not a table rebuild, so the table's
-- sqlite_sequence survives.
DELETE FROM `fact_observations`;
DELETE FROM `fact_coverages`;
DELETE FROM `facts`;
ALTER TABLE `facts` ADD COLUMN `key_token` text NOT NULL DEFAULT ('');
ALTER TABLE `facts` ADD COLUMN `keyed_by` text NOT NULL DEFAULT ('');
DROP INDEX `fact_key_device_facts`;
CREATE INDEX `fact_key_token_device_facts` ON `facts` (`key_token`, `device_facts`);
