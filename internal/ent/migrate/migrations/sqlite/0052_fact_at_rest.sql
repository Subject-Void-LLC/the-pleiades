-- Facts at rest: a stored fact names nothing in plaintext. It is a contract
-- (internal/ent/migrate's compat.go), not applied while a build before it is
-- still running (guard.go). A row is found by key_token and says which keys
-- made it in keyed_by, and payload holds the key and the value sealed. Every
-- fact stored before this is plaintext and carries a plain hash, which no key
-- can turn into a token or a keyed hash, so those rows are deleted with their
-- observations; the next gather stores each device's facts again, sealed.
-- The deleted rows' bytes are then removed from the file by a VACUUM this
-- migration cannot run itself (internal/ent/migrate's sqliteAfterCommit).
-- The schema itself refuses a build before it, so one the guard could not
-- see stores nothing: key is dropped, which every statement that build sends
-- to facts names, and the fact_keyed check refuses a row whose key_token or
-- keyed_by is empty, which is every row a writer knowing neither column
-- inserts. Its coverage and observation rows are written only after those,
-- in the same transaction, so they are never stored either.
-- Written as ALTER TABLE, not a table rebuild, so the table's
-- sqlite_sequence survives. The keyed_by index is what a change of the facts
-- key lists the devices still under the previous key by.
DELETE FROM `fact_observations`;
DELETE FROM `fact_coverages`;
DELETE FROM `facts`;
ALTER TABLE `facts` ADD COLUMN `key_token` text NOT NULL DEFAULT ('');
ALTER TABLE `facts` ADD COLUMN `keyed_by` text NOT NULL DEFAULT ('') CONSTRAINT `fact_keyed` CHECK (key_token <> '' AND keyed_by <> '');
DROP INDEX `fact_key_device_facts`;
ALTER TABLE `facts` DROP COLUMN `key`;
CREATE INDEX `fact_key_token_device_facts` ON `facts` (`key_token`, `device_facts`);
CREATE INDEX `fact_keyed_by_device_facts` ON `facts` (`keyed_by`, `device_facts`);
