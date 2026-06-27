# syntax=docker/dockerfile:1

# --- build stage ---
FROM golang:1.22-alpine AS build
WORKDIR /src

# Cache modules first (stdlib-only today, but keeps the layer stable).
COPY go.mod ./
RUN go mod download

COPY . .
# Static, stripped binary for a scratch final image.
RUN CGO_ENABLED=0 GOOS=linux go build -trimpath -ldflags="-s -w" \
    -o /out/snapreq ./cmd/snapreq

# --- final stage ---
# distroless static, non-root (uid 65532) — no shell, no package manager.
FROM gcr.io/distroless/static:nonroot
COPY --from=build /out/snapreq /snapreq
EXPOSE 8080
USER nonroot:nonroot
ENTRYPOINT ["/snapreq"]
