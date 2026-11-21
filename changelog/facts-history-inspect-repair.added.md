`controller facts inspect` reports, for one device (`--device` by name, `--device-id` by the ID the log names) or
every device whose stored fact history the controller cannot serve (such as one a change of
`PLEIADES_FACTS_DIGEST_KEY` refused to re-key), which keys its rows name, whether it verifies under the current key,
the previous one or both, and the first row that fails and why, naming no fact or value (`--json` prints
`docs/reference/schemas/facts-inspection.json`). It changes nothing, works with either facts key alone, never creates,
migrates or vacuums a database, and refuses a `MASTER_ENCRYPTION_KEY` the database's key registry does not record.
`controller facts truncate` keeps the history before the gather that wrote the last value that verifies and re-keys
it; `controller facts discard` removes the device's whole history so its next gather starts again, and takes
`--discard-unverifiable-history` for a history the keys given cannot verify. Both show what they would do until
`--confirm`, and record themselves in the activity stream with a keyed digest of what they removed. A device's facts
routes now answer 503, naming the command, when one of its stored facts does not open as its own, where they answered
500; the start-up rotation log names the command too.
