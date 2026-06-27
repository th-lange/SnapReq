# Snap Requests

SnapReq is a lightweight Go HTTP capture sidecar — **Part 1 of the SnapReq +
EchoChamber system**. Its only job is to receive inbound HTTP requests, serialise
them to the shared ingest schema, and **fire-and-forget** them at EchoChamber's
`POST /internal/ingest`, never adding latency to the hot path.

See [Agent.md](Agent.md) for the full design rules.

## Getting started

Prerequisite: Go 1.22+.

### 1. Build

```sh
go build -o snapreq ./cmd/snapreq
```

### 2. Give captures somewhere to land

SnapReq fires every captured request at EchoChamber's `POST /internal/ingest`.
For a quick local try-out you can stand in a throwaway receiver that just prints
what it gets and replies `202 Accepted`:

```sh
python3 - <<'PY'
from http.server import BaseHTTPRequestHandler, HTTPServer
class H(BaseHTTPRequestHandler):
    def do_POST(self):
        body = self.rfile.read(int(self.headers.get('Content-Length', 0)))
        print("INGEST", self.headers.get('Authorization'), body.decode(), flush=True)
        self.send_response(202); self.end_headers()
    def log_message(self, *a): pass
HTTPServer(('127.0.0.1', 8081), H).serve_forever()
PY
```

(In a real deployment this is the running EchoChamber service.)

### 3. Run SnapReq (mirror mode)

In a second terminal, point SnapReq at the receiver and start it:

```sh
ECHOCHAMBER_URL=http://127.0.0.1:8081 \
ECHOCHAMBER_TOKEN=dev-token \
./snapreq
```

You'll see it log the active mode on startup:

```
level=INFO msg="starting snapreq" mode=mirror echochamber_url=http://127.0.0.1:8081 listen_addr=:8080 ...
level=INFO msg=listening addr=:8080
```

### 4. Send your first request

```sh
curl -i -X POST 'http://localhost:8080/v1/widgets?id=7' \
  -H 'Content-Type: application/json' \
  -d '{"hello":"world"}'
```

SnapReq responds immediately with `204 No Content` (it never waits on the
capture):

```
HTTP/1.1 204 No Content
```

…and your receiver prints the captured request — note the `Bearer` token and
that EchoChamber, not SnapReq, will stamp `capturedAt`:

```
INGEST Bearer dev-token {"method":"POST","uri":"http://localhost:8080/v1/widgets?id=7","authority":"localhost:8080","headers":{"Accept":"*/*","Content-Length":"17","Content-Type":"application/json","User-Agent":"curl/8.18.0"},"body":"{\"hello\":\"world\"}"}
```

That's the whole loop: **request in → `204` out → captured copy fired at
EchoChamber.**

### Try proxy mode (Mode A)

Set `FORWARD_URL` to put SnapReq in the request path. It forwards to the upstream
and returns the upstream's response, while still capturing on the side:

```sh
ECHOCHAMBER_URL=http://127.0.0.1:8081 \
ECHOCHAMBER_TOKEN=dev-token \
FORWARD_URL=https://httpbin.org \
./snapreq

curl -i http://localhost:8080/get   # returns httpbin's response; capture still fires
```

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

## Development

```sh
go build ./...                     # build everything
go test ./... -race                # unit + integration tests, race-clean
go vet ./...
staticcheck ./...                  # go install honnef.co/go/tools/cmd/staticcheck@latest
```

### Docker

The image is a non-root distroless static binary. SnapReq attaches to the
`reexec-net` bridge created by EchoChamber's compose, so start EchoChamber first,
then bring SnapReq up (host `9090` → container `:8080`):

```sh
docker compose up --build
```

Container config is read from the committed [`.env`](.env) (sensible, non-secret
dev defaults — `ECHOCHAMBER_URL=http://app:8080`, etc.). For personal or secret
overrides, create a `.env.local` (gitignored); its values win. The dev
`ECHOCHAMBER_TOKEN` must match EchoChamber's `INTERNAL_INGEST_TOKEN`.

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
