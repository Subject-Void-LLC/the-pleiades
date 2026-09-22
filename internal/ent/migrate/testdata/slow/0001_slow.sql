-- A migration slow enough to cut its connection in the middle of, used only by
-- chaos_internal_test.go.
SELECT pg_sleep(4);
CREATE TABLE slow_migration_marker (id integer PRIMARY KEY);
