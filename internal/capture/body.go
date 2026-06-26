// Package capture holds the SnapReq hot path: body buffering and the inbound
// http.Handler that serves every request across all deployment modes.
package capture

import (
	"io"
	"log/slog"
	"net/http"
)

// ReadBody reads up to maxBytes from the request body, returning the bytes read,
// whether the body was truncated, and any read error. The body is always closed.
//
// Empty bodies take a fast path that allocates nothing: a nil/NoBody body or a
// declared Content-Length of 0 returns (nil, false, nil).
//
// On a partial read error the bytes read so far are returned alongside the error
// so the caller can still capture what it has (Agent.md §6).
func ReadBody(r *http.Request, maxBytes int64) ([]byte, bool, error) {
	if r.Body == nil || r.Body == http.NoBody || r.ContentLength == 0 {
		if r.Body != nil {
			_ = r.Body.Close()
		}
		return nil, false, nil
	}
	defer r.Body.Close()

	// Read one byte past the limit so we can detect truncation.
	limited := io.LimitReader(r.Body, maxBytes+1)
	data, err := io.ReadAll(limited)
	if err != nil {
		return data, false, err
	}

	truncated := int64(len(data)) > maxBytes
	if truncated {
		data = data[:maxBytes]
		slog.Warn("request body truncated for capture",
			slog.Int64("limit_bytes", maxBytes),
			slog.Bool("exceeds_limit", true))
	}
	return data, truncated, nil
}

// ReadFullBody reads the entire request body for Mode A, where the full body
// must reach the upstream regardless of MAX_BODY_BYTES. It returns the full body
// (to forward), a capture copy truncated to maxBytes, and whether truncation
// occurred. The body is buffered once — capture reuses the same bytes, never a
// second read. The body is always closed.
func ReadFullBody(r *http.Request, maxBytes int64) (full, capture []byte, truncated bool, err error) {
	if r.Body == nil || r.Body == http.NoBody || r.ContentLength == 0 {
		if r.Body != nil {
			_ = r.Body.Close()
		}
		return nil, nil, false, nil
	}
	defer r.Body.Close()

	full, err = io.ReadAll(r.Body)
	if err != nil {
		return full, capForCapture(full, maxBytes), false, err
	}
	truncated = int64(len(full)) > maxBytes
	if truncated {
		slog.Warn("request body truncated for capture",
			slog.Int64("limit_bytes", maxBytes),
			slog.Int("body_bytes", len(full)))
	}
	return full, capForCapture(full, maxBytes), truncated, nil
}

func capForCapture(full []byte, maxBytes int64) []byte {
	if int64(len(full)) > maxBytes {
		return full[:maxBytes]
	}
	return full
}

// bodyPtr converts captured bytes into the *string expected by IngestPayload:
// nil when there is no body, otherwise a pointer to the string form.
func bodyPtr(data []byte) *string {
	if data == nil {
		return nil
	}
	s := string(data)
	return &s
}
