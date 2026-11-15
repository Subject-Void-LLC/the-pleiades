`net.netconf.config`'s `content` may name a stored secret, such as an IKE pre-shared key, with
`{{ secret.<name>.<field> }}` (`pleiades add-secret`): it is resolved just before the session opens, escaped as XML, and
masked in everything the run reports, so a NETCONF-only design no longer has to put a key on the CLI.
