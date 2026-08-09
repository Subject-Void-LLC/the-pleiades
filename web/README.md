# Pleiades web UI

The Crawl-tier web console: a React 19 + Vite single-page app. `Dockerfile.web` builds
it to static files and nginx serves them on port 80, next to the Controller but not by
it.

**Status: a mockup, all six routes.** Five render hardcoded content and make no
network request at all. The sixth, `/jobs/:id`, is the only file under `src/` that
contacts the API, and it cannot reach a running Controller. No page in this app
reaches `/api/v1` today.

| Route | File | State |
|---|---|---|
| `/` | `src/views/Dashboard.tsx` | Placeholder, hardcoded numbers |
| `/inventories` | `src/views/Inventories.tsx` | Placeholder |
| `/runbooks` | `src/views/Runbooks.tsx` | Placeholder |
| `/jobs/:id` | `src/views/JobDetails.tsx` | An SSE log stream viewer with search and autoscroll. Real streaming code, broken wiring: see below |
| `/governance` | `src/views/Governance.tsx` | Placeholder |
| `/credentials` | `src/views/Credentials.tsx` | Placeholder |

## Why the log viewer cannot connect

Three separate defects, each enough on its own.

1. **It ignores its own route parameter.** `JobDetails.tsx` sets
   `const jobId = "123"` and never calls `useParams()`, so every visit to
   `/jobs/<anything>` asks for job `"123"`. The API validates that ID as a UUID and
   answers `400 {"error":"job id must be a UUID"}`.
2. **It calls the wrong address, and nothing forwards.** The URL is hardcoded to
   `http://localhost:8081`. `cmd/controller` listens on `:8080` unless `LISTEN_ADDR`
   overrides it, `vite.config.ts` declares no `server.proxy`, and `nginx.conf` serves
   static files with no `/api` location. Port 8081 is `cmd/demo`'s, and `cmd/demo`
   wires the same authentication, so pointing there does not help.
3. **`EventSource` cannot authenticate.** Every route under `/api/v1` requires an
   `Authorization: Bearer <token>` header. An `EventSource` cannot be given request
   headers: its constructor takes a URL and a `withCredentials` flag, nothing more.
   The API reads a token from nowhere else either, no cookie and no query parameter,
   so the request arrives anonymous and is rejected 401 before the handler runs. That
   401 carries no `Access-Control-Allow-Origin` header, so the browser will not
   release it to the page. The `Access-Control-Allow-Origin: *` in
   `internal/api/logs.go` is set inside the handler, which the request never reaches.

A fix needs all three: read the ID from the router, send the request through a
same-origin `/api` proxy, and give the API a token path a browser event stream can
actually use. The endpoint itself works. `curl -N` with an admin Bearer token and a
real job UUID returns `200 text/event-stream` and an `event: init` frame.

## Running it

```bash
npm install
npm run dev       # local dev server with HMR
npm run build     # production build
npm run lint      # oxlint
```

The dev server serves this app and nothing else. It declares no API proxy, so a
Controller running locally changes nothing about what these pages show; see "Why the
log viewer cannot connect" above. `npm run build` writes to `dist/`, which is
gitignored: any `dist/` in a checkout is a stale local artifact, not a shipped bundle.
