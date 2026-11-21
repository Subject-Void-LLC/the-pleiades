`controller facts inspect` reports, for one device (`--device` by name, `--device-id` by the ID the log names) or
every device whose stored fact history the controller cannot serve (such as one a change of
`PLEIADES_FACTS_DIGEST_KEY` refused to re-key), which keys its rows name, which master key versions they are sealed
as and whether it was given a key for each, whether the history verifies under the current key, the previous one or
both, and the first row that fails and why, naming no fact or value (`--json` prints
`docs/reference/schemas/facts-inspection.json`). It changes nothing, works with either facts key alone, never creates,
migrates or vacuums a database, and refuses a `MASTER_ENCRYPTION_KEY` the database's key registry does not record;
it warns when the registry records a newer master key than the ones given, a key under another version than it was
given as, or no record of `MASTER_ENCRYPTION_KEY_PREVIOUS`. A row that does not open is never by itself taken as
damage, since a row sealed under a master key or key version the command was not given looks the same: a device
with no row that opens reads as `unopened`, one with some as `partly_unopened`, never `broken`, each with the advice
to check the master key settings, and each repair of a device with any row that does not open takes a second flag.
`controller facts truncate` keeps the history before the gather that wrote the last value that verifies, removes
that gather whole and everything after it, removes a failing volatile value alone (a volatile fact holds one value,
so its gather confirms nothing in its place), and re-keys what it keeps; it is not offered when it would keep
nothing, refuses a history that is `unopened`, and needs `--truncate-unverifiable-history` for one with a row that
does not open. `controller facts discard` removes the device's whole history so its next gather starts again, and
needs `--discard-unverifiable-history` for a history the keys given cannot verify (a row that does not open, or rows
under another facts key or both). Both show what they would do until `--confirm`, and record themselves in the
activity stream with a keyed digest of what they removed. A device's facts routes now answer 503, naming the
command, when one of its stored facts does not open as its own, where they answered 500; the start-up rotation log
names the command too.
