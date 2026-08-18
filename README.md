# Beach Club

The shore report for your cluster. A [theme theme](https://konstruct.civo.com/docs/next/konduit)
for [Konstruct](https://konstruct.civo.com) that pairs zone/app data from the
platform with deep Kubernetes observability from
[groundcover](https://groundcover.civo.io) — eBPF golden signals, cluster
health, events, and logs, with zero instrumentation.

The tide on the hero is drawn by the cluster itself: wave height tracks CPU
load, and the conditions line reads **clean / choppy / blown out** from live
issues and failed pods.

## What's on the page

| Section | What it shows | Source |
|---|---|---|
| The Shore Report | conditions, nodes, pods riding, wipeouts | groundcover metrics |
| Swell Gauges | CPU, memory, network over 1h/6h/24h | groundcover PromQL |
| The Lineup | per-workload latency, error rate, traffic | groundcover eBPF APM |
| Wipeout Log | issues and warning events | groundcover events |
| Beach Patrol | recent error/warning logs | groundcover logs |
| Beaches & Boards | zones and apps as beaches and surfboards | theme v2 |

Every panel deeplinks into the groundcover UI at `GROUNDCOVER_UI_URL`.

## Run it locally

```sh
go run .
# open http://localhost:8080 — demo mode: sample beaches, captured cluster data
```

Live mode:

```sh
export GROUNDCOVER_API_KEY=<service-account key>
export GC_CLUSTER=<cluster name as groundcover knows it>
go run .
```

## Configuration

| Env var | Default | Purpose |
|---|---|---|
| `PORT` | `8080` | listen port (set by the platform) |
| `GROUNDCOVER_API_URL` | `https://ingest.groundcover.civo.io` | groundcover inCloud backend |
| `GROUNDCOVER_API_KEY` | *(empty → sample mode)* | service-account key, injected via the platform's `env[]` — never commit it |
| `GROUNDCOVER_TENANT_UUID` | civo.com tenant | tenant routing header |
| `GROUNDCOVER_BACKEND_ID` | `groundcover` | backend routing header |
| `GROUNDCOVER_UI_URL` | `https://groundcover.civo.io` | deeplink target for every panel |
| `GC_CLUSTER` | `konstruct-control-plane-jd-relentless-today` | the cluster to report on |

## Refresh the sample data

`sample-data.json` is a real snapshot so the demo mode looks like a real
beach. Regenerate it against a live backend:

```sh
GROUNDCOVER_API_KEY=<key> go run ./cmd/capture
```

## Registering on Konstruct

Register this repository as a theme in Konstruct Settings, then set
`GROUNDCOVER_API_KEY` (and `GC_CLUSTER` if different) in the theme's
environment. `static/theme.js` is copied byte-for-byte from
[theme-starter](https://github.com/konstructio/theme-starter)
and is verified at registration — never edit it.
