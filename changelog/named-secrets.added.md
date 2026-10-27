Named secrets: `pleiades add-secret <name> --field <field>` stores a value a configuration needs that is no device's
login (an IKE pre-shared key several routers share, a CA's passphrase), encrypted like a device credential, and a
runbook names it as `{{ secret.<name>.<field> }}` in `net.ios.config`'s `lines` or a `prompts` answer. The method fills
it in just before the device reads it; validation refuses a reference anywhere else, and every named secret is masked
by value in everything a run reports. `list-secrets` and `remove-secret` complete the set. Proven on a CML lab: two
IOL XE routers negotiate IKEv2 on a key from `add-secret`, and no report, journal file or output holds it.
