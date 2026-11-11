-- The inventory's version (internal/ent/schema/inventory.go), added in place
-- rather than by rebuilding the table, which is what the generator wrote. A
-- rebuild drops the table and so its AUTOINCREMENT high-water mark: the id of
-- the most recently deleted inventory would be handed out again, and a role
-- binding still scoped to the deleted one (scope_id has no foreign key) would
-- grant the new one. ADD COLUMN keeps the table, its sequence and its rows.
ALTER TABLE `inventories` ADD COLUMN `version` integer NOT NULL DEFAULT (0);
