The controller now serves HTTPS. Point `TLS_CERT_FILE` and `TLS_KEY_FILE` at a real
certificate, or set `PLEIADES_TLS_TERMINATED_UPSTREAM=1` when an ingress in front of it
already terminates TLS; setting one of the pair without the other, or both arrangements
at once, is a startup error. The `PLEIADES_UI_INSECURE_COOKIES` opt-out is gone with the
code behind it, so the browser session cookie is always `Secure` and `__Host-` prefixed.
This fixes sign-in reporting "Those credentials were not accepted" for a correct password
whenever the controller was reached by a hostname or LAN address rather than `localhost`.
