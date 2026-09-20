Upgrading an existing embedded SQLite database is now safe: schema migrations that rebuild a
table really do suspend foreign-key enforcement, and each one is refused if it would leave a row
pointing at nothing. Before this fix, upgrading a database that held data could silently delete
every saved survey question and saved launch configuration, and a database holding a schedule or a
finished job could not be upgraded at all. If you have already upgraded past version 0022, check
your templates for missing survey questions and saved launch configurations, and re-create any
that are gone.
