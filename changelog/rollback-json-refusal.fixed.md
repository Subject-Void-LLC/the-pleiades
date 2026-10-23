Fixed `pleiades rollback --json` refusing with only a count of problems in `outcome.message`: the message now
carries each problem's reason and the flag that accepts it, as the text view prints them, beside the full list in
`rollback.problems`.
