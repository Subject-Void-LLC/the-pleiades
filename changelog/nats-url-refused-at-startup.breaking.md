`NATS_URL` is now checked before the controller binds its port or migrates its
database, rather than when it first dials the broker. A value with no scheme, a
scheme the client library does not implement, or a list mixing encrypted and
plaintext brokers now stops the process immediately instead of producing one that
answers its health probe for a few seconds and then exits. Deployments passing a
bare `host:port` have to add `nats://` or `tls://`.

The Helm chart applies the same rule to `externalNats.url`, so an install with a
mistyped scheme is refused by the values schema rather than at broker connect
time.
