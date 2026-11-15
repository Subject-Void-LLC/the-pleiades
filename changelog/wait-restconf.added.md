`wait.restconf` waits until a device's RESTCONF server answers its account with the capability list under its root,
since HTTPS answering is not readiness (a Catalyst 8000V answered 502 under `/restconf` for minutes after its web server
did). Each try is a new connection; an untrusted certificate ends the wait before any request, and a refused login after
one attempt.
