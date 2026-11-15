The `yang.*` facts are protocol-neutral: `yang.modules`, `yang.module_count` and `yang.modules_digest` now leave out the
modules a protocol's server lists for itself (`ietf-netconf`, `ietf-netconf-with-defaults` and `tailf-netconf-extensions`
over NETCONF, `tailf-rest-query` over RESTCONF, measured on a Catalyst 8000V), which `netconf.modules` and
`restconf.modules` record instead, so a device reads alike over both. Every NETCONF device's recorded values change once
(559 modules to 556 on that router), and the onboarding records' `yang_module_count` and `yang_modules_digest` with them;
onboarding records the protocols' own modules beside them as `netconf_modules` and `restconf_modules`. A new fact,
`yang.digest_rule` (and `yang_digest_rule` in the onboarding record), names the list of protocol modules the digest was
computed under, `protocol-modules-` and 12 hex digits that change whenever the list does: compare two
`yang.modules_digest` values only when their rules are equal. A Controller older than this release stores
`yang.digest_rule`, `netconf.modules` and the `restconf.*` facts a newer Runner sends as it stores any fact its
module catalog does not declare, with the kind the Runner sent, and refuses none of them.
