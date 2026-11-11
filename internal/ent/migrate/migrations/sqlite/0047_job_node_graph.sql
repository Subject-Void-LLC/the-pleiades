-- The graph a job's first fan-out pinned (internal/ent/schema/job.go's
-- dag_version and node_graph), added in place, as the generator wrote it: an
-- ADD COLUMN keeps the table, its AUTOINCREMENT sequence and its rows, where a
-- rebuild would drop the sequence (see 0046_inventory_version.sql).
PRAGMA foreign_keys = off;
ALTER TABLE `jobs` ADD COLUMN `dag_version` text NULL;
ALTER TABLE `jobs` ADD COLUMN `node_graph` json NULL;
PRAGMA foreign_keys = on;
