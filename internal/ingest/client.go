package ingest

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"
)

// Client delivers captured payloads to EchoChamber's ingest endpoint. It wraps a
// single connection-pooled *http.Client and is initialised once at startup, then
// reused for every capture. It never retries (Agent.md §6).
type Client struct {
	baseURL string
	token   string // never logged
	http    *http.Client
}

// NewClient constructs an ingest Client. The timeout is a safety net on the
// shared http.Client; the primary deadline is the per-call context (Send).
func NewClient(echochamberURL, token string, timeout time.Duration) *Client {
	return &Client{
		baseURL: strings.TrimRight(echochamberURL, "/"),
		token:   token,
		http: &http.Client{
			Timeout: timeout,
		},
	}
}

// Send serialises payload and POSTs it to <baseURL>/internal/ingest with a Bearer
// token. The supplied ctx carries the CAPTURE_TIMEOUT_MS deadline. A non-2xx
// response is returned as an error; there are no retries.
func (c *Client) Send(ctx context.Context, payload IngestPayload) error {
	body, err := json.Marshal(payload)
	if err != nil {
		return fmt.Errorf("marshal ingest payload: %w", err)
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, c.baseURL+"/internal/ingest", bytes.NewReader(body))
	if err != nil {
		return fmt.Errorf("build ingest request: %w", err)
	}
	req.Header.Set("Authorization", "Bearer "+c.token)
	req.Header.Set("Content-Type", "application/json")

	resp, err := c.http.Do(req)
	if err != nil {
		return fmt.Errorf("send ingest request: %w", err)
	}
	defer resp.Body.Close()
	// Drain so the connection can be reused from the pool.
	_, _ = io.Copy(io.Discard, resp.Body)

	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return fmt.Errorf("ingest returned non-2xx status: %d", resp.StatusCode)
	}
	return nil
}
