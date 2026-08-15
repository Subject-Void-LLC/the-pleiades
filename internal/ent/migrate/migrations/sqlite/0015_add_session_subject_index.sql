PRAGMA foreign_keys = off;
CREATE INDEX `session_subject` ON `sessions` (`subject`);
PRAGMA foreign_keys = on;
