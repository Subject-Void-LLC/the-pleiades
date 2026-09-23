Enabling `nats.websocket.tls` without `nats.tls.enabled` used to render a broker
that could not start: its configuration named the certificate files while nothing
mounted them. Serving `wss://` on the WebSocket listener while the in-cluster
client listener stays plaintext now works as intended.
