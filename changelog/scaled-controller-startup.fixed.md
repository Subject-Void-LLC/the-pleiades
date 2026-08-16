Several controllers sharing one certificate directory now all start, and none of them
waits for another. Previously, when replicas started at the same instant against a shared
volume, each one could refuse to start with "could not read a usable pair back", or report
that it was listening and then exit with "private key does not match public key". A
controller now writes the certificate and its key as a single file, which is replaced in
one step, so replicas do not have to take turns: each one reads what is published, writes
only if there is nothing usable there, and reads again. A replica that is slow, or that is
killed while writing, no longer stops any other replica from starting, and a directory
that becomes read-only no longer does either. The startup log also reports why a
certificate was replaced, and the "controller listening" line is printed after the
listener binds rather than before.

The certificate directory now holds `serving.pem` (the certificate and its private key,
which is what the controller serves), `cert.pem` (the same certificate with no key in it,
which is the file to hand a client) and `provisioned/` (one record per certificate this
deployment has provisioned, which the container healthcheck trusts). A directory written
by an earlier version is picked up as it is: the certificate already there is kept rather
than replaced, and its `key.pem` is retired once the same key is safely inside
`serving.pem`. Copy `cert.pem` to a client, never `serving.pem`.

A private key in `PLEIADES_TLS_AUTOCERT_DIR` that this controller cannot prove it wrote is
now never written over or deleted, including when there is no certificate beside it, which
is what a half-finished secret mount looks like. A certificate and key an operator places
there are served as they are and never replaced. That protection now covers a private key
and nothing else, which is what stops it from bricking a directory: a `serving.pem` that is
empty, damaged or truncated, and a `cert.pem` with no key beside it, are neither a secret
nor servable, so a controller starts, provisions and serves instead of refusing forever.
Those bytes are still never overwritten. A file that exists and cannot be READ is still a
refusal, because its contents are unknown, and the message now names the file, quotes what
the operating system said and gives the user id this controller runs as, rather than
calling it somebody else's material.

The container healthcheck no longer restarts a replica that is serving perfectly. A
certificate record is kept until the certificate in it expires, rather than being evicted
once sixteen newer ones exist, and a controller that reuses a published certificate records
that it is serving it, so a sibling publishing a new certificate cannot leave a running
replica unverifiable. A legacy `provisioned.pem` that cannot be read now costs the probe
one trust anchor instead of failing it outright.

The startup log renamed two fields, because one of them was wrong. `cert_file` used to
carry the path of `serving.pem`, which holds the private key; the fields are now
`serving_file` (the material, key included) and `trust_anchor_file` (the same certificate
with no key in it, present only when such a copy exists). The headline no longer describes
an operator's own certificate as one this controller provisioned for itself, and a first
start reports "no certificate is stored in ... yet" rather than a raw `stat` error. An expired certificate named by
`TLS_CERT_FILE` is still served, and the controller now says so loudly rather than failing
every handshake in silence. A name `PLEIADES_TLS_AUTOCERT_HOSTS` cannot put on a
certificate, such as an international domain written in its display form, is reported at
startup with the variable named.

`PLEIADES_TLS_TERMINATED_UPSTREAM` now accepts `true`, `yes` and `on` as well as `1`
(and `false`, `no`, `off` as well as `0`), and refuses to start on any other value. A
controller behind an ingress that forwards plain HTTP used to ignore
`PLEIADES_TLS_TERMINATED_UPSTREAM=true` and serve HTTPS anyway.
