`controller facts inspect` reports, for one device or every device whose stored fact history the controller cannot
serve (such as one a change of `PLEIADES_FACTS_DIGEST_KEY` refused to re-key), which keys its rows name, whether it
verifies under the current key, the previous one or both, and the first row that fails and why, naming no fact or
value (`--json` prints `docs/reference/schemas/facts-inspection.json`). It changes nothing and works with either key
alone. `controller facts truncate` keeps the history before the first failure and re-keys it; `controller facts
discard` removes the device's whole history so its next gather starts again. Both show what they would do until
`--confirm`, and record themselves in the activity stream with a keyed digest of what they removed. The 503 a refused
device's facts routes answer, and the start-up rotation log, name the command.
