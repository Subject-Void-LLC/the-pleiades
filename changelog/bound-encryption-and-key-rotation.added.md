Device connection properties and saved survey answers are now encrypted with their
ciphertext cryptographically bound to the row it belongs to, which credentials have had
since they were introduced. A value copied from one row to another no longer decrypts.

Two new key rotation passes cover credentials and saved launch configurations, alongside
the one devices already had. Each re-encrypts under the current master key and, on the
way past, converts any row still in the older unbound form.

Rows written by an earlier release keep working and are read normally, so an upgrade
needs no downtime and loses nothing. Until a rotation pass has run, though, those rows
keep the older form and remain copyable between rows: the passes report how many rows
they converted, which is how you tell the migration is finished.

Saved launch configurations are the ones to run a pass for. Nothing else in the platform
ever rewrites their answers, so unlike devices they never migrate themselves.
