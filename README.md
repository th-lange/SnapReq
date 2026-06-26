# Snap Requests

SnapReq is a lightweight Go HTTP capture sidecar — **Part 1 of the SnapReq +
EchoChamber system**. Its only job is to receive inbound HTTP requests, serialise
them to the shared ingest schema, and **fire-and-forget** them at EchoChamber's
`POST /internal/ingest`, never adding latency to the hot path.

See [Agent.md](Agent.md) for the full design rules.

## Deployment modes

One binary, selected by environment at startup (no runtime switching):

| Mode | Trigger | Behaviour |
|---|---|---|
| **A — In-Path Proxy** | `FORWARD_URL` set | Forwards to the upstream and returns its response; captures in a goroutine fired *before* the forward dial. |
| **B — Mirror / Tap** | `FORWARD_URL` unset | Receives a mirrored copy, returns `204 No Content` immediately, captures async. |
| **D — Standalone Receiver** | `FORWARD_URL` unset | Same as Mode B from SnapReq's perspective; the distinction is in the caller's routing. |

## Configuration

All via environment variables (see [`.env.example`](.env.example)):

| Variable | Required | Default | Description |
|---|---|---|---|
| `ECHOCHAMBER_URL` | Yes | — | Base URL of EchoChamber |
| `ECHOCHAMBER_TOKEN` | Yes | — | Bearer token matching EchoChamber's `INTERNAL_INGEST_TOKEN` |
| `LISTEN_ADDR` | No | `:8080` | Listen address |
| `FORWARD_URL` | No | — | If set, activates Mode A (upstream base URL) |
| `FORWARD_TIMEOUT_MS` | No | `5000` | Mode A forward-leg timeout |
| `CAPTURE_TIMEOUT_MS` | No | `2000` | Fire-and-forget ingest timeout |
| `MAX_BODY_BYTES` | No | `1048576` | Max body bytes captured (truncated, not dropped) |
| `LOG_LEVEL` | No | `info` | `debug`, `info`, `warn`, `error` |
| `CAPTURE_AUTH_HEADERS` | No | `false` | Keep `Authorization` in captured payloads |

## Build & run

```sh
# Build
go build ./cmd/snapreq

# Run (mirror mode)
ECHOCHAMBER_URL=http://localhost:8081 ECHOCHAMBER_TOKEN=dev-token ./snapreq

# Run (proxy mode)
ECHOCHAMBER_URL=http://localhost:8081 ECHOCHAMBER_TOKEN=dev-token \
  FORWARD_URL=http://localhost:9000 ./snapreq
```

### Tests

```sh
go test ./... -race
go vet ./...
staticcheck ./...    # go install honnef.co/go/tools/cmd/staticcheck@latest
```

### Docker

```sh
docker compose up --build
```

## Project layout

```
cmd/snapreq/main.go          entrypoint: config, wiring, server, graceful shutdown
internal/config/             env parsing + validation (once at startup)
internal/ingest/payload.go   IngestPayload — the shared contract (source of truth)
internal/ingest/client.go    fire-and-forget POST to /internal/ingest
internal/capture/body.go     body buffering + truncation + empty-body fast path
internal/capture/handler.go  the http.Handler hot path (all modes)
internal/forward/client.go   Mode A upstream proxy client
```
