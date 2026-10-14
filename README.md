# pulso

[![ci](https://github.com/KlGamers/pulso/actions/workflows/ci.yml/badge.svg)](https://github.com/KlGamers/pulso/actions/workflows/ci.yml)

A self-hosted uptime monitor that ships as one binary. It probes HTTP and TCP
endpoints on a schedule, keeps the history in SQLite, posts to a webhook when a
service goes down or comes back, and serves a status page.

![Status page](docs/status-dark.png)

## Why

Hosted monitors bill per check and want an account for every teammate. The
self-hosted ones usually need a database server and a queue. pulso needs a JSON
file and a place to write one SQLite file.

## Quick start

```sh
go install github.com/KlGamers/pulso/cmd/pulso@latest
cp pulso.example.json pulso.json
pulso -c pulso.json
```

Open http://localhost:8080.

With Docker:

```sh
docker build -t pulso .
docker run -p 8080:8080 -v "$PWD:/data" pulso
```

## Configuration

```json
{
  "listen": ":8080",
  "db": "pulso.db",
  "title": "Acme Status",
  "keep": "720h",
  "workers": 8,
  "alert": { "url": "https://hooks.example.com/pulso", "fail_after": 3, "recover_after": 2 },
  "targets": [
    { "name": "website", "addr": "https://example.com", "every": "30s" },
    { "name": "api", "addr": "https://api.example.com/health", "expect": 200, "match": "\"ok\"" },
    { "name": "postgres", "kind": "tcp", "addr": "db.internal:5432", "timeout": "3s" }
  ]
}
```

| Field | Default | Meaning |
|---|---|---|
| `listen` | `:8080` | Address for the status page and API |
| `db` | `pulso.db` | SQLite file |
| `keep` | `720h` | How long check history is kept |
| `workers` | `8` | Maximum probes running at once |
| `alert.url` | none | Webhook that receives state changes |
| `alert.fail_after` | `3` | Consecutive failures before a target is down |
| `alert.recover_after` | `2` | Consecutive successes before it is up again |
| `targets[].kind` | `http` | `http` or `tcp` |
| `targets[].every` | `30s` | Probe interval |
| `targets[].timeout` | `5s` | Probe timeout, must not exceed `every` |
| `targets[].expect` | any 2xx or 3xx | Exact status code required |
| `targets[].match` | none | Substring the response body must contain |

The config is validated at startup. A bad file stops the process with the name
of the offending target.

## Alerts

A target flips to down only after `fail_after` failures in a row, and back to
up after `recover_after` successes, so one dropped packet does not page anyone.
Each flip posts once:

```json
{ "kind": "down", "target": "api", "at": "2026-01-10T08:15:04Z", "reason": "status 502", "fails": 3 }
```

Failed deliveries are retried with exponential backoff. Open incidents survive
a restart, so a service that was already down is not announced twice.

## API

| Route | Returns |
|---|---|
| `GET /api/targets` | State, last check, 24h and 7d uptime, average and p95 latency |
| `GET /api/targets/{name}/checks?n=100` | Most recent checks, oldest first |
| `GET /api/incidents` | Last 50 incidents |
| `GET /healthz` | `ok` |

## How it works

```
cfg ──> sched ──> probe ──> store (SQLite, WAL)
          │                    ▲
          └──> alert ──> webhook
                               │
web  <─────────────────────────┘
```

- `sched` starts one goroutine per target. Each waits a deterministic jitter
  derived from the target name, then ticks at its own interval. A semaphore
  caps concurrent probes at `workers`.
- `probe` reuses one HTTP transport, bounds every request with a context
  deadline and reads at most 1 MiB of body.
- `alert` is a small state machine per target. It is the only place that
  decides whether a result is an incident.
- `store` uses the pure Go SQLite driver, so the binary has no C dependency
  and cross-compiles with `CGO_ENABLED=0`.
- `web` renders one embedded template and exposes the same data as JSON.

## Development

```sh
make test
make lint
make build
```

Tests run against `httptest` servers and temporary databases. Nothing touches
the network.

## Limits

- One process, one SQLite file. There is no clustering and no multi-region
  probing.
- HTTP probes send `GET` only.
- The status page has no authentication. Put it behind your proxy if it should
  not be public.

## License

MIT
