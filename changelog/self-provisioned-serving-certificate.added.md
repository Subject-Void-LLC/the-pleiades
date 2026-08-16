A controller started with no TLS settings now generates a self-signed certificate,
stores it, and reuses it on later starts, instead of refusing to start. `docker compose
up -d --wait` therefore works from a clean checkout with no preparatory command.
`PLEIADES_TLS_AUTOCERT_DIR` chooses where the pair is stored (default `tls`, relative to
the working directory) and `PLEIADES_TLS_AUTOCERT_HOSTS` adds subject alternative names
beyond `localhost`, the loopback addresses and the host's own name. The certificate
encrypts traffic but does not authenticate the server, so browsers warn and the
controller logs that plainly on every start: it is a convenience, not a replacement for
`TLS_CERT_FILE` and `TLS_KEY_FILE`, which still win whenever they are set.
