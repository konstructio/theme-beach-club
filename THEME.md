# THEME
version: v2
theme: beach-club
capabilities: [apps, zones, quota, runtime-logs, app-events]
vocabulary:
  zone: { singular: beach, plural: beaches, verb: claim }
  app: { singular: board, plural: boards, verb: "paddle out" }
  deploy: { verb: "paddle out" }

## The contract (v2 — postMessage transport)

Beach Club is an informational theme: it reads zones and apps through
`theme.js` (byte-for-byte from the starter; never edited) and renders them
as beaches and boards. It performs no writes.

Everything else on the page — cluster health, golden signals, events, and
logs — comes from this theme's own backend at `/api/gc/*`, which proxies a
groundcover inCloud backend using an API key injected by the platform's
`env[]`. No credential ever reaches the browser; the frontend renders every
API-derived string with `textContent`.

- Launched from Konstruct: sandboxed iframe, org from the query string,
  operations over postMessage. No token, no fragment, nothing in
  sessionStorage.
- Opened directly (`theme.isLaunched()` is false): the theme renders its
  demo tide pool — sample beaches and boards — and the groundcover panels
  serve captured sample data when no API key is configured.

Allowed ops used by this theme: `discover`, `zones`, `apps`.
