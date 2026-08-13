The API now accepts a session cookie in addition to `Authorization: Bearer`, resolved by
one middleware into one identity. Existing API and CLI clients are unaffected; a supplied
bearer token always wins over a cookie.

This is what makes the live job log stream reachable from a browser. A browser's
`EventSource` cannot send request headers at all, so while a bearer token was the only
credential accepted, no browser client could open that stream regardless of anything
else.
