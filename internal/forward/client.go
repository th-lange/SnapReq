// Package forward implements the Mode A upstream proxy leg. It uses its own
// shared http.Client, separate from the ingest client (Agent.md §7).
package forward

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"
)

// hopByHopHeaders are connection-scoped headers that must not be forwarded to the
// upstream (Agent.md §5).
var hopByHopHeaders = []string{
	"Connection",
	"Keep-Alive",
	"Proxy-Authenticate",
	"Proxy-Authorization",
	"Proxy-Connection",
	"TE",
	"Trailer",
	"Transfer-Encoding",
	"Upgrade",
}

// Client forwards inbound requests to a configured upstream base URL.
type Client struct {
	targetBase string
	http       *http.Client
}

// NewClient constructs a forward Client with an explicit timeout on the forward
// leg (FORWARD_TIMEOUT_MS).
func NewClient(targetURL string, timeout time.Duration) *Client {
	return &Client{
		targetBase: strings.TrimRight(targetURL, "/"),
		http: &http.Client{
			Timeout: timeout,
		},
	}
}

// Forward builds an upstream request from the inbound request r, preserving
// method, path, query, and (non-hop-by-hop) headers, and sends the supplied
// pre-buffered body. The caller responds 502 on error.
func (c *Client) Forward(ctx context.Context, r *http.Request, body []byte) (*http.Response, error) {
	target := c.targetBase + r.URL.RequestURI()

	var rdr io.Reader
	if body != nil {
		rdr = bytes.NewReader(body)
	}

	upstream, err := http.NewRequestWithContext(ctx, r.Method, target, rdr)
	if err != nil {
		return nil, fmt.Errorf("build upstream request: %w", err)
	}
	copyForwardHeaders(r.Header, upstream.Header)
	if body != nil {
		upstream.ContentLength = int64(len(body))
	}

	resp, err := c.http.Do(upstream)
	if err != nil {
		return nil, fmt.Errorf("upstream request failed: %w", err)
	}
	return resp, nil
}

func copyForwardHeaders(src, dst http.Header) {
	for name, values := range src {
		if isHopByHop(name) {
			continue
		}
		for _, v := range values {
			dst.Add(name, v)
		}
	}
}

func isHopByHop(name string) bool {
	for _, h := range hopByHopHeaders {
		if strings.EqualFold(name, h) {
			return true
		}
	}
	return false
}
