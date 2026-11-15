The `yang.*` facts are protocol-neutral: `yang.modules`, `yang.module_count` and `yang.modules_digest` now leave out the
modules a protocol's server lists for itself (`ietf-netconf`, `ietf-netconf-with-defaults` and `tailf-netconf-extensions`
over NETCONF, `tailf-rest-query` over RESTCONF, measured on a Catalyst 8000V), which `netconf.modules` and
`restconf.modules` record instead, so a device reads alike over both. Every NETCONF device's recorded values change once
(559 modules to 556 on that router), and the onboarding summaries' `yang_module_count` and `yang_modules_digest` with them.
