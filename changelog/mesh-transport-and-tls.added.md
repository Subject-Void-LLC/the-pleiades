The message bus can now be reached over TLS or WebSocket. Point `NATS_URL` at
`tls://` or `wss://`, set `NATS_CA_FILE` if your broker uses a private authority,
and enable `nats.tls` or `nats.websocket` in the Helm chart for the bundled broker.
WebSocket is for reaching a broker through a proxy or an egress filter that only
allows 443; it does not make a connection more tolerant of a dropped link.

`NATS_URL` is now checked at startup. A bare host and port with no scheme used to
be silently treated as unencrypted, and a list mixing encrypted and plaintext
brokers used to be accepted; both are refused now.
