-- Expand-only: runner_seed on mesh_signing_keys (broker-enforced Runner
-- identity). Written as ALTER TABLE, not a table rebuild, so the table's
-- sqlite_sequence survives.
ALTER TABLE `mesh_signing_keys` ADD COLUMN `runner_seed` text NOT NULL DEFAULT ('');
