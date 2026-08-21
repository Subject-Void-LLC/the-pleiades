Added 11 structured-data filters, callable from `when`, `when_or`, and
`when_cel`: `flatten`/`unflatten` (dot-notation key conversion),
`deepMerge`/`shallowMerge`, `csvToList`/`listToCSV` (one CSV line, correct
quote handling), `pluck` (extract one key's value across a list of maps),
`yamlToJSON`/`jsonToYAML`, `generateUUIDv4`, and `xmlToJSON` (one documented
element/attribute convention). See the generated filter reference for the
full list.
