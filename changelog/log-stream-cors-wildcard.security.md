Removed `Access-Control-Allow-Origin: *` from the job log stream. The header existed so a
separate front-end development server on another port could read the stream, back when
the only credential the endpoint accepted was a bearer token a script had to attach
deliberately.

Both of those changed: the UI is now served from the same origin, and the endpoint now
accepts an ambient session cookie. A wildcard origin on a cookie-authenticated stream is
a permission for any site a signed-in operator visits to read what their automation is
doing. Browsers refuse to combine a wildcard origin with credentials, so this was not
exploitable as written, but it was one header away from being so.
