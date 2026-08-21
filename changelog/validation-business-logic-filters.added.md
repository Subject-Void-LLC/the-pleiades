Added 17 validation and business-logic filters, callable from `when`,
`when_or`, and `when_cel`: `isValidFQDN`, `isValidEmail`, `isValidUUID`,
`isValidBase64`, `isValidJSON`, `isValidYAML`, `isValidPort`,
`dropEmptyValues`, `filterListByKV`/`excludeListByKV`, `listContains`,
`hasMandatoryTags` (returns the missing keys, not just a bool),
`listIntersect`/`listDiff`, `dedupeByKey`, `compareSemVer`, and
`isValidCronExpr` (a small, hand-rolled 5-field cron parser, not a
scheduling mechanism of this platform's own). See the generated filter
reference for the full list.
