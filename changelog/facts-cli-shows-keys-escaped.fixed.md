`pleiades facts` now shows a stored fact key that would not show as itself (one holding a zero width or other
invisible character, a line separator, a tag character or a newline) with each such character written as `\u{...}`,
in its text and `--json` views alike, so no key reads as another or starts a line of its own. The reason
`pleiades facts --verify` gives for a broken chain is escaped for the terminal too.
