`net.ios.command` and `net.cli.command` now mask secrets in XML and JSON output (`| format netconf-xml`,
`| format restconf-json`) and any crypt-shaped hash wherever it appears; before, those forms returned local secrets as
raw type 9 hashes. Structured output that does not parse is not returned.
