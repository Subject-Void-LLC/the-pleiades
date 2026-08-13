Added the Activity Stream: an append-only record of who changed which managed object,
and when, readable in the web UI and over `GET /api/v1/activity`.

The platform already decided this and kept none of it. Administrative writes were
recorded nowhere at all, so "who put that subject in the admin team last Thursday" had
no answer in the system. Every create, update, delete and attestation of an
organization, team, user, role binding or contact now leaves a line naming the
authenticated subject that made it.

It is written by a store decorator wrapped once at the composition root, not by calls
placed in request handlers, so both write surfaces are covered: the JSON API, and the
web UI's own writers, which reach the store directly and pass through no handler. A
write that arrives with no identifiable actor is refused rather than recorded against
nobody, because an audit trail containing anonymous rows looks complete to whoever
reads it.

Each entry captures the object's name as it was at the time and carries no foreign key
to the object itself, so the record of a deletion outlives the thing it describes and
still says what that thing was called.
