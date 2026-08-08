# Pleiades web UI

The Crawl-tier web console: a React 19 + Vite single-page app served alongside the
Controller. Talks to the Controller's REST API under `/api/v1` (see
`internal/api/router.go`).

**Status: mostly a mockup.** Of the six routes below, only `/jobs/:id` does anything
real.

| Route | File | State |
|---|---|---|
| `/` | `src/views/Dashboard.tsx` | Placeholder, hardcoded numbers |
| `/inventories` | `src/views/Inventories.tsx` | Placeholder |
| `/runbooks` | `src/views/Runbooks.tsx` | Placeholder |
| `/jobs/:id` | `src/views/JobDetails.tsx` | Real: an SSE log stream viewer with search and autoscroll |
| `/governance` | `src/views/Governance.tsx` | Placeholder |
| `/credentials` | `src/views/Credentials.tsx` | Placeholder |

## Running it

```bash
npm install
npm run dev       # local dev server with HMR
npm run build     # production build
npm run lint      # oxlint
```

The dev server expects a Controller running and reachable; see the repository root
`docs/` for how to start one.
