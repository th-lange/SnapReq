# Agent Rules — SnapReq

SnapReq is a lightweight Go HTTP interceptor / capture sidecar (Part 1 of the SnapReq + EchoChamber system).  
Its **only job**: receive inbound HTTP requests, serialise them to the shared ingest schema, and fire them at EchoChamber — as fast as possible, with no waiting.

Read this file fully before writing any code. Apply every rule on every task.

**Task tracking:** All tasks are tracked via the GitHub project board.

---

## 0. The Prime Directive

SnapReq must **never add latency to the hot path**.

Every design decision is subordinate to this. If a feature adds synchronous work, block time, or retry logic on the capture path, it is wrong. Capture is always fire-and-forget.

---

## 1. Deployment Modes

SnapReq supports three deployment modes, selected by environment variables. The same binary handles all three.

### Mode A — In-Path Proxy (`FORWARD_URL` is set)
```
Client → SnapReq → Upstream (FORWARD_URL)
                ↘ EchoChamber (fire-and-forget, goroutine)
```
- SnapReq forwards the request to `FORWARD_URL` and returns the upstream response to the client.
- The capture to EchoChamber happens in a separate goroutine — it **never** blocks the forward path.
- Request body must be buffered once and shared between forward and capture. Use `io.NopCloser` on a `bytes.Buffer`.
- Timeout for the forward leg is controlled by `FORWARD_TIMEOUT_MS` (default: `5000`).
- Capture failures are logged and silently dropped — they must never affect the forward response.

### Mode B — Mirror / Tap (`FORWARD_URL` is not set, running as mirror target)
```
Primary proxy (Envoy / nginx) → SnapReq (mirror copy)
SnapReq → EchoChamber (fire-and-forget)
```
- SnapReq receives a mirrored copy of the request. It does not need to return a meaningful response.
- Always returns `204 No Content` immediately after reading the request.
- No body buffering for forward — buffer only for capture.

### Mode D — Standalone Receiver (`FORWARD_URL` is not set, receiving forwarded requests)
```
External proxy → SnapReq
SnapReq → EchoChamber (fire-and-forget)
```
- Identical to Mode B from SnapReq's perspective. The distinction is in the caller's routing, not SnapReq's code.
- Returns `204 No Content` immediately.

**Rule:** Mode is determined at startup. There is no runtime switching. Log the active mode clearly on startup.

---

## 2. Architecture

```
cmd/
  snapreq/
    main.go          ← flag/env parsing, mode detection, server bootstrap

internal/
  capture/
    handler.go       ← http.Handler: reads request, triggers capture goroutine, responds
    body.go          ← body buffering helpers (shared read between forward + capture)
  forward/
    client.go        ← HTTP client for Mode A forward leg
  ingest/
    client.go        ← HTTP client for EchoChamber /internal/ingest (fire-and-forget)
    payload.go       ← IngestPayload struct + serialisation (shared contract)
  config/
    config.go        ← all env-var parsing in one place, validated at startup
```

**Rules:**
- `internal/` packages must not import each other circularly.
- `ingest/payload.go` is the **source of truth** for the shared contract — it defines the JSON shape sent to EchoChamber.
- No package may import a framework. Standard library + `net/http` only.
- `main.go` wires dependencies. No logic in `main.go` beyond wiring and `log.Fatal`.

---

## 3. The Ingest Payload (Shared Contract)

This struct defines the JSON sent to EchoChamber's `POST /internal/ingest`. It must stay in sync with EchoChamber's ingest DTO. Any change here requires a corresponding change there.

```go
// internal/ingest/payload.go
type IngestPayload struct {
    Method    string            `json:"method"`
    URI       string            `json:"uri"`       // full URI including scheme+host if present
    Authority string            `json:"authority"` // host:port
    Headers   map[string]string `json:"headers"`   // single value per header name (last wins)
    Body      *string           `json:"body"`      // nil if no body or body not captured
}
```

**Rules:**
- `Headers` collapses multi-value headers to a single string (last value wins). This is a deliberate simplification.
- `Body` is `nil`, not an empty string, when there is no body.
- The `CapturedAt` timestamp is **not** set by SnapReq — EchoChamber stamps it on receipt. SnapReq has no clock responsibility.
- Never add fields to this struct without updating EchoChamber's ingest DTO simultaneously.

---

## 4. Configuration

All configuration via environment variables. Parsed and validated once at startup in `internal/config/config.go`. Fatal exit on missing required config.

| Variable | Required | Default | Description |
|---|---|---|---|
| `ECHOCHAMBER_URL` | Yes | — | Base URL of EchoChamber, e.g. `http://echochamber:8080` |
| `ECHOCHAMBER_TOKEN` | Yes | — | Bearer token matching EchoChamber's `INTERNAL_INGEST_TOKEN` |
| `LISTEN_ADDR` | No | `:8080` | Address SnapReq listens on |
| `FORWARD_URL` | No | — | If set, activates Mode A. Target upstream base URL. |
| `FORWARD_TIMEOUT_MS` | No | `5000` | Mode A only: forward leg timeout in milliseconds |
| `CAPTURE_TIMEOUT_MS` | No | `2000` | Timeout for the fire-and-forget POST to EchoChamber |
| `MAX_BODY_BYTES` | No | `1048576` | Max body bytes to capture (1 MiB). Bodies exceeding this are truncated, not dropped. |
| `LOG_LEVEL` | No | `info` | `debug`, `info`, `warn`, `error` |

**Rules:**
- No config file format. Env vars only.
- `ECHOCHAMBER_TOKEN` must never be logged at any level.
- Log all other resolved config values at startup at `info` level.

---

## 5. Capture Rules

1. Read the request body up to `MAX_BODY_BYTES`. If body exceeds limit, capture truncated bytes and log a `warn`. In Mode A, the full body must still be forwarded — buffer the full body for forward, truncated copy for capture.
2. Collapse multi-value headers to single values (last wins). Strip hop-by-hop headers (`Connection`, `Transfer-Encoding`, `Keep-Alive`, `Upgrade`, `Proxy-*`) from the captured headers map.
3. `Authority` is derived from the `Host` header, falling back to `r.Host`.
4. `URI` is the full request URI: if the request came in as an absolute URI use it as-is; otherwise construct `scheme://authority+path?query`. Scheme is inferred from `X-Forwarded-Proto` header if present, otherwise `http`.
5. Fire the capture goroutine **before** starting the forward leg in Mode A, so capture is not delayed by upstream latency. Use `context.WithTimeout` with `CAPTURE_TIMEOUT_MS`.
6. The capture goroutine must recover from panics and log them. It must never propagate a panic to the main goroutine.

---

## 6. Error Handling

- **Forward errors (Mode A):** Return `502 Bad Gateway` to the client. Log at `error`.
- **Capture errors (all modes):** Log at `warn`. Silently discard. Never affect the HTTP response.
- **Config errors at startup:** `log.Fatal`. Non-negotiable.
- **Body read errors:** Log at `warn`, capture what was read so far, continue.

No retries on capture. SnapReq is fast because it doesn't retry. EchoChamber's idempotent ingest handles duplicates if the caller retries at a higher level.

---

## 7. Performance Rules

- Use a single shared `http.Client` for EchoChamber calls (connection pooling). Initialise once at startup.
- Use a single shared `http.Client` for forward calls in Mode A. Separate instance from the capture client.
- Set explicit timeouts on both clients. Never use the default (no timeout) client.
- Do not allocate a `bytes.Buffer` on every request if the body is empty. Check `Content-Length: 0` and `r.Body == http.NoBody` first.
- Goroutine leak prevention: the capture goroutine must always terminate (timeout or completion). Use `context.WithTimeout`.

---

## 8. Test Requirements

| Component | Required test |
|---|---|
| `ingest/payload.go` | Unit: serialise/deserialise round-trip, nil body, empty headers |
| `capture/handler.go` Mode B/D | Unit: returns 204, fires goroutine, does not block |
| `capture/handler.go` Mode A | Unit: forwards to upstream, capture is non-blocking |
| `capture/body.go` | Unit: body truncation at MAX_BODY_BYTES, empty body, exact-limit body |
| `config/config.go` | Unit: missing required vars fatal, defaults applied correctly |
| `ingest/client.go` | Unit with mock HTTP server: correct auth header, correct path, fire-and-forget |
| Integration | Start SnapReq in each mode against a mock upstream + mock EchoChamber; verify payload shape |

Use `net/http/httptest` for all HTTP testing. No external test frameworks.

---

## 9. Security Rules

- `ECHOCHAMBER_TOKEN` is sent as `Authorization: Bearer <token>` on every ingest call.
- Never log request bodies at `info` level. Log body size only.
- Never log the auth token.
- Strip `Authorization` headers from captured requests before sending to EchoChamber — the ingest endpoint has its own auth; captured auth headers are sensitive and should not be double-stored unless explicitly configured via `CAPTURE_AUTH_HEADERS=true`.

---

## 10. What Not To Do

- Do not add retry logic to the capture path.
- Do not wait for EchoChamber to respond before returning to the caller (Mode A) or responding 204 (Mode B/D).
- Do not use a framework (Gin, Echo, Chi, etc.). Standard `net/http` only.
- Do not add business logic. SnapReq does not decide what to store — that is EchoChamber's job via drop rules.
- Do not version the ingest payload format inside SnapReq. Versioning is EchoChamber's responsibility.
- Do not use `init()` functions.
- Do not use global mutable state outside of the initialised-once clients in `main.go`.

---

## 11. GitHub Ticket Workflow

- **Before starting work** on a ticket, move it to **"In Progress"** on the project board.
- **When complete**, open a Pull Request referencing the ticket (`Closes #<n>`). Move ticket to **"In Review"**.
- PR description must summarise: what changed, why, and any relevant decisions.

### 11.1 Definition of Done — Mandatory after every task

1. Self-evaluate against the ticket's Acceptance Criteria and the §12 Self-Correction Checklist.
2. Run `go test ./...` and confirm all tests pass. Call out pre-existing failures explicitly.
3. Run `go vet ./...` and `staticcheck ./...` — zero warnings.
4. Verify no framework imports leaked in.
5. If ready: open PR via `gh pr create` against `main`. Do not wait to be asked.
6. Report PR URL and evaluation summary back to the user in the same response.

---

## 12. Self-Correction Checklist

- [ ] Does the capture path block the response in any code path? → **Fix it.**
- [ ] Is `ECHOCHAMBER_TOKEN` logged anywhere? → **Remove it.**
- [ ] Is `Authorization` from the original request passed through to EchoChamber payload without `CAPTURE_AUTH_HEADERS=true`? → **Strip it.**
- [ ] Does the capture goroutine have a timeout? → **Add `context.WithTimeout`.**
- [ ] Does the capture goroutine recover from panics? → **Add `recover()`.**
- [ ] Is there a shared HTTP client (not created per-request)? → **Fix it.**
- [ ] Does `ingest/payload.go` match EchoChamber's ingest DTO exactly? → **Verify against EchoChamber's contract section.**
- [ ] Are hop-by-hop headers stripped from the captured payload? → **Verify.**
- [ ] Is mode logged clearly at startup? → **Add it.**
- [ ] Does Mode A buffer the body correctly for both forward and capture? → **Verify.**

---

## 13. Self-Update Rules (Meta)

This file is a living document. The agent maintaining this codebase **must** follow these rules about updating it:

**Add freely:** When a pattern, constraint, or decision is discovered during implementation that would prevent future mistakes or clarify intent, add it to the relevant section without asking. New sections may be added. Examples: a discovered edge case in body buffering, a Go version constraint, a performance gotcha.

**Propose before removing or changing existing rules:** If an existing rule appears outdated, incorrect, or in conflict with a new finding, do **not** silently delete or change it. Instead, append a clearly marked proposal block at the end of the file:

```
## PROPOSED CHANGE — <date>
**Rule affected:** §N <rule name>
**Reason:** <what was discovered>
**Proposed change:** <exact new wording or deletion>
**Awaiting human approval.**
```

Leave the original rule intact until a human explicitly approves the change.

**Never propose changes to §0 (The Prime Directive) or §10 (What Not To Do) without an exceptional justification.** These are load-bearing constraints.
