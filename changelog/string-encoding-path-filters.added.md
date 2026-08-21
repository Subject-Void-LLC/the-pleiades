Added 16 string, encoding, and path filters, callable from `when`, `when_or`,
and `when_cel`: `urlEncode`/`urlDecode`, `camelToSnake`/`snakeToCamel`,
`stringToHex`/`hexToString`, `regexExtract` (named capture group extraction),
`maskSecret`, `windowsPathToPOSIX`/`posixPathToWindows`,
`octalToSymbolicPerms`/`symbolicToOctalPerms`, `bytesToHuman`/`humanToBytes`
(binary base-1024), and `isAbsolutePath`/`isEmptyOrWhitespace`. A prefix,
suffix, substring, or regex match test needs no new filter: CEL's own
`startsWith`/`endsWith`/`contains`/`matches` string methods already cover
that ground. See the generated filter reference for the full list.
