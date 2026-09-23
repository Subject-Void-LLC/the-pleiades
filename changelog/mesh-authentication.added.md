The message bus can now authenticate its clients. `controller mesh init` mints the
key hierarchy and writes the broker's configuration, `controller mesh issue` mints
a credential for a controller or a runner, and each process presents one by setting
`NATS_CREDS_FILE`. The Helm chart gains `nats.auth` and `mesh.credentials`, and
compose gains a `docker-compose.mesh-auth.yml` overlay. It is off by default, so an
existing deployment upgrades unchanged.

A credential expires, and a process whose credential lapses is disconnected by the
broker and stops reconnecting, so replace one before its expiry. Authentication
decides who may read the message stream, not what is written to it: a dispatch
still carries the credentials its job runs with.
