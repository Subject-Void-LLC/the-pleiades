A project's source is now fetched over https or ssh only. Plain http, the git daemon protocol
(`git://`) and paths on the server's own disk are refused unless the deployment sets
`PLEIADES_PROJECT_ALLOW_INSECURE_SOURCE` or `PLEIADES_PROJECT_ALLOW_LOCAL_SOURCE`, because the content
of a sync is code this platform then runs on managed devices. A password in the URL is refused with no
toggle: give the project a credential instead, which is stored encrypted. An existing project keeps
syncing until somebody saves it.
