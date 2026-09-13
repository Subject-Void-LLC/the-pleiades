Pleiades can now mint short-lived, subject-scoped NATS credentials for its own processes, and
a broker can be configured to require one. A Controller credential may provision the stream and
fan work out; a Runner credential may pull its work, take a device lease, publish logs and
results, and nothing else. A compromised Runner cannot forge a job launch, and cannot reshape
the shared dispatch consumer to read another device's work.

This is a capability, not a default. Nothing in the shipped chart or compose file requires
authentication yet, so an existing deployment is unaffected and needs no change.

Two things an operator turning this on needs to know, both measured against a real broker
rather than inferred:

An operator-mode deployment needs TWO accounts, not one. NATS refuses to start JetStream at
all without a system account, and that system account must not itself have JetStream enabled.
Pleiades mints both, and keeps them distinct in its own API so the difference cannot be
configured away by accident.

A credential is enforced on a LIVE connection, not only at connect time. When one expires, the
broker evicts the connection holding it, and the client stops rather than reconnecting forever.
That is deliberate: a Runner whose identity has lapsed should stop taking work. It surfaces as
a single "nats connection closed permanently" error line, which is the one signal that
distinguishes a lapsed identity from a Runner that is quietly idle.
