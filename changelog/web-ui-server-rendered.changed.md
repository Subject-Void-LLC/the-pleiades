The controller now serves the web UI itself, at `/ui`, from assets built into the
binary. The separate React application, its Node build chain, and the `web` service in
`docker-compose.yml` are gone. There is no front-end build step and no second container:
browse to the controller's own address and add `/ui`.

Sign in at `/ui/login` by pasting a token this control plane already accepts; it is
exchanged for a session cookie. Signing out revokes that session immediately, and a
session works against any controller replica sharing the database, so no sticky-session
load balancer is needed.

Six views are registered. The dashboard, inventories, jobs and runbooks views are real
and read live data. Governance and credentials are registered but not implemented, and
say so on the page rather than rendering an empty table. Device properties are still not
editable, jobs cannot be cancelled, and runbooks are read-only; each page states its own
limits.

The UI is usable on a phone for the common tasks, offers light and dark themes plus a
high-contrast mode, and can display an administrator-configured environment or
classification banner. See `docs/12-web-ui.md`.

The controller binary is about 6 MB larger, because the charting library and the
hypermedia library it uses are embedded rather than fetched from a network.
