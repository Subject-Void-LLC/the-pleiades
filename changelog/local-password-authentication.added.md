The web UI now accepts an email and a password at `/ui/login`, alongside the existing
token paste. Create the first account on the host with `controller bootstrap-admin
--email you@example.com`; `controller reset-password` and `controller unlock` are the
recovery paths, and a signed-in operator can change their own password at
`/ui/account`.

Passwords are hashed with Argon2id and are never recoverable. A wrong password, an
unknown address and a locked account are indistinguishable to the caller and cost the
same amount of time. Ten consecutive failures lock an account for fifteen minutes,
counted in the database rather than per process, so the limit is not multiplied by
running more replicas or reset by a restart.

Token paste is unchanged and remains the break-glass route, and the only route for a
deployment that federates against an external issuer and holds no local passwords.

Changing a password revokes every other session that account holds and keeps the one
doing the changing. Deleting a user now revokes its sessions and removes its password
in the same operation; previously a deleted user's cookie kept working until its
absolute deadline.
