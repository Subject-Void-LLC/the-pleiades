Added filter functions callable from a `when`, `when_or`, or `when_cel` condition:
`filters.safeInt`, `filters.safeFloat`, and `filters.safeBool` parse a value that may be
present but malformed, falling back to a default instead of failing the run. `stat.?field.orValue(...)`
now works too, the same missing-value default Ansible's own `default` filter provides.
IP, CIDR, and base64/JSON helpers (`ip()`, `cidr()`, `base64.encode`, `json.encode`, and more)
are also available. See the generated filter reference for the full list.
