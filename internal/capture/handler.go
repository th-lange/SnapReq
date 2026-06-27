package capture

import (
	"context"
	"io"
	"log/slog"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/th-lange/snapreq/internal/ingest"
)

// Sender delivers a captured payload to EchoChamber. Implemented by
// *ingest.Client; an interface here keeps the handler testable.
type Sender interface {
	Send(ctx context.Context, payload ingest.IngestPayload) error
}

// Forwarder proxies an inbound request to the configured upstream (Mode A).
// Implemented by *forward.Client.
type Forwarder interface {
	Forward(ctx context.Context, r *http.Request, body []byte) (*http.Response, error)
}

// Handler is the http.Handler registered for every inbound request. When
// forwarder is non-nil it runs Mode A (proxy); otherwise Mode B/D (mirror).
type Handler struct {
	ingest             Sender
	forwarder          Forwarder // nil in mirror mode
	maxBodyBytes       int64
	captureTimeout     time.Duration
	captureAuthHeaders bool
}

// New constructs a Handler. Pass a non-nil forwarder for Mode A; pass nil for
// Mode B/D.
func New(sender Sender, forwarder Forwarder, maxBodyBytes int64, captureTimeout time.Duration, captureAuthHeaders bool) *Handler {
	return &Handler{
		ingest:             sender,
		forwarder:          forwarder,
		maxBodyBytes:       maxBodyBytes,
		captureTimeout:     captureTimeout,
		captureAuthHeaders: captureAuthHeaders,
	}
}

func (h *Handler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	if h.forwarder != nil {
		h.serveProxy(w, r)
		return
	}
	h.serveMirror(w, r)
}

// serveMirror handles Mode B/D: capture asynchronously, respond 204 immediately.
func (h *Handler) serveMirror(w http.ResponseWriter, r *http.Request) {
	body, _, err := ReadBody(r, h.maxBodyBytes)
	if err != nil {
		slog.Warn("error reading request body", slog.String("err", err.Error()))
	}
	h.captureAsync(h.buildPayload(r, body))
	w.WriteHeader(http.StatusNoContent)
}

// serveProxy handles Mode A: fire capture before dialling upstream, then forward
// and relay the upstream response. The Prime Directive (Agent.md §0) requires the
// capture goroutine to start before the forward dial.
func (h *Handler) serveProxy(w http.ResponseWriter, r *http.Request) {
	full, capture, _, err := ReadFullBody(r, h.maxBodyBytes)
	if err != nil {
		slog.Warn("error reading request body", slog.String("err", err.Error()))
	}

	// Capture first — never delayed by upstream latency.
	h.captureAsync(h.buildPayload(r, capture))

	resp, err := h.forwarder.Forward(r.Context(), r, full)
	if err != nil {
		slog.Error("forward to upstream failed", slog.String("err", err.Error()))
		http.Error(w, "bad gateway", http.StatusBadGateway)
		return
	}
	defer resp.Body.Close()

	copyResponseHeaders(w.Header(), resp.Header)
	w.WriteHeader(resp.StatusCode)
	_, _ = io.Copy(w, resp.Body)
}

// captureAsync fires the fire-and-forget capture goroutine. It uses a detached
// context (not the request context, which is cancelled once the handler returns)
// with its own CAPTURE_TIMEOUT_MS deadline and recovers from panics. A dropped
// capture (delivery failure or panic) is logged at CRITICAL — it signals data
// loss to operators but must never affect the HTTP response (Agent.md §0, §5, §6).
func (h *Handler) captureAsync(payload ingest.IngestPayload) {
	go func() {
		defer func() {
			if p := recover(); p != nil {
				logCritical("dropped captured request: panic in capture goroutine", slog.Any("panic", p))
			}
		}()

		ctx, cancel := context.WithTimeout(context.Background(), h.captureTimeout)
		defer cancel()

		if err := h.ingest.Send(ctx, payload); err != nil {
			logCritical("dropped captured request: ingest delivery failed (EchoChamber unreachable?)",
				slog.String("err", err.Error()))
		}
	}()
}

func (h *Handler) buildPayload(r *http.Request, body []byte) ingest.IngestPayload {
	authority := r.Header.Get("Host")
	if authority == "" {
		authority = r.Host
	}
	return ingest.IngestPayload{
		Method:    r.Method,
		URI:       buildURI(r, authority),
		Authority: authority,
		Headers:   h.captureHeaders(r.Header),
		Body:      bodyPtr(body),
	}
}

// captureHeaders collapses multi-value headers (last wins), strips hop-by-hop
// headers, and strips Authorization unless capture is configured to keep it.
func (h *Handler) captureHeaders(src http.Header) map[string]string {
	out := make(map[string]string, len(src))
	for name, values := range src {
		if isCaptureStripped(name, h.captureAuthHeaders) {
			continue
		}
		if len(values) > 0 {
			out[name] = values[len(values)-1]
		}
	}
	return out
}

// buildURI returns the full request URI. Absolute-form request targets are used
// as-is; otherwise scheme://authority/path?query is constructed with the scheme
// taken from X-Forwarded-Proto (fallback http).
func buildURI(r *http.Request, authority string) string {
	if r.URL.IsAbs() {
		return r.URL.String()
	}
	scheme := r.Header.Get("X-Forwarded-Proto")
	if scheme == "" {
		scheme = "http"
	}
	u := url.URL{
		Scheme:   scheme,
		Host:     authority,
		Path:     r.URL.Path,
		RawQuery: r.URL.RawQuery,
	}
	return u.String()
}

func isCaptureStripped(name string, keepAuth bool) bool {
	if !keepAuth && strings.EqualFold(name, "Authorization") {
		return true
	}
	switch {
	case strings.EqualFold(name, "Connection"),
		strings.EqualFold(name, "Transfer-Encoding"),
		strings.EqualFold(name, "Keep-Alive"),
		strings.EqualFold(name, "Upgrade"),
		strings.HasPrefix(strings.ToLower(name), "proxy-"):
		return true
	}
	return false
}

func copyResponseHeaders(dst, src http.Header) {
	for name, values := range src {
		for _, v := range values {
			dst.Add(name, v)
		}
	}
}
