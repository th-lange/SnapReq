package capture

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/th-lange/snapreq/internal/ingest"
)

// recordingSender captures payloads delivered by the async capture goroutine.
type recordingSender struct {
	ch    chan ingest.IngestPayload
	panic bool
}

func newRecordingSender() *recordingSender {
	return &recordingSender{ch: make(chan ingest.IngestPayload, 1)}
}

func (s *recordingSender) Send(_ context.Context, p ingest.IngestPayload) error {
	if s.panic {
		panic("boom")
	}
	s.ch <- p
	return nil
}

func (s *recordingSender) wait(t *testing.T) ingest.IngestPayload {
	t.Helper()
	select {
	case p := <-s.ch:
		return p
	case <-time.After(2 * time.Second):
		t.Fatal("capture payload not received")
		return ingest.IngestPayload{}
	}
}

// stubForwarder returns a canned upstream response and records the body it saw.
type stubForwarder struct {
	gotBody  []byte
	status   int
	respBody string
	err      error
}

func (f *stubForwarder) Forward(_ context.Context, _ *http.Request, body []byte) (*http.Response, error) {
	f.gotBody = body
	if f.err != nil {
		return nil, f.err
	}
	return &http.Response{
		StatusCode: f.status,
		Body:       io.NopCloser(strings.NewReader(f.respBody)),
		Header:     http.Header{"X-Upstream": []string{"yes"}},
	}, nil
}

func TestServeMirror_Returns204AndCaptures(t *testing.T) {
	sender := newRecordingSender()
	h := New(sender, nil, 1024, time.Second, false)

	req := httptest.NewRequest(http.MethodPost, "http://svc.example/v1/x?q=1", strings.NewReader("payload"))
	req.Host = "svc.example"
	rec := httptest.NewRecorder()

	h.ServeHTTP(rec, req)

	if rec.Code != http.StatusNoContent {
		t.Errorf("status = %d, want 204", rec.Code)
	}
	p := sender.wait(t)
	if p.Method != "POST" {
		t.Errorf("method = %q", p.Method)
	}
	if p.Authority != "svc.example" {
		t.Errorf("authority = %q", p.Authority)
	}
	if p.Body == nil || *p.Body != "payload" {
		t.Errorf("body = %v", p.Body)
	}
}

func TestServeProxy_ForwardsAndCaptures(t *testing.T) {
	sender := newRecordingSender()
	fwd := &stubForwarder{status: 201, respBody: "upstream-ok"}
	h := New(sender, fwd, 1024, time.Second, false)

	req := httptest.NewRequest(http.MethodPost, "http://svc.example/api", strings.NewReader("hello"))
	req.Host = "svc.example"
	rec := httptest.NewRecorder()

	h.ServeHTTP(rec, req)

	if rec.Code != 201 {
		t.Errorf("status = %d, want 201", rec.Code)
	}
	if rec.Body.String() != "upstream-ok" {
		t.Errorf("body = %q", rec.Body.String())
	}
	if rec.Header().Get("X-Upstream") != "yes" {
		t.Error("upstream headers not relayed")
	}
	if string(fwd.gotBody) != "hello" {
		t.Errorf("forwarded body = %q, want full body", fwd.gotBody)
	}
	p := sender.wait(t)
	if p.Body == nil || *p.Body != "hello" {
		t.Errorf("captured body = %v", p.Body)
	}
}

func TestServeProxy_ForwardFailureReturns502ButStillCaptures(t *testing.T) {
	sender := newRecordingSender()
	fwd := &stubForwarder{err: io.ErrUnexpectedEOF}
	h := New(sender, fwd, 1024, time.Second, false)

	req := httptest.NewRequest(http.MethodGet, "http://svc.example/x", nil)
	req.Host = "svc.example"
	rec := httptest.NewRecorder()

	h.ServeHTTP(rec, req)

	if rec.Code != http.StatusBadGateway {
		t.Errorf("status = %d, want 502", rec.Code)
	}
	_ = sender.wait(t) // capture still fired
}

func TestCapture_StripsAuthorizationByDefault(t *testing.T) {
	sender := newRecordingSender()
	h := New(sender, nil, 1024, time.Second, false)

	req := httptest.NewRequest(http.MethodGet, "http://svc.example/x", nil)
	req.Header.Set("Authorization", "Bearer secret")
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)

	p := sender.wait(t)
	if _, ok := p.Headers["Authorization"]; ok {
		t.Error("Authorization should be stripped by default")
	}
}

func TestCapture_KeepsAuthorizationWhenEnabled(t *testing.T) {
	sender := newRecordingSender()
	h := New(sender, nil, 1024, time.Second, true)

	req := httptest.NewRequest(http.MethodGet, "http://svc.example/x", nil)
	req.Header.Set("Authorization", "Bearer secret")
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)

	p := sender.wait(t)
	if p.Headers["Authorization"] != "Bearer secret" {
		t.Errorf("Authorization = %q, want kept", p.Headers["Authorization"])
	}
}

func TestCapture_StripsHopByHopHeaders(t *testing.T) {
	sender := newRecordingSender()
	h := New(sender, nil, 1024, time.Second, false)

	req := httptest.NewRequest(http.MethodGet, "http://svc.example/x", nil)
	req.Header.Set("Connection", "keep-alive")
	req.Header.Set("Proxy-Authorization", "x")
	req.Header.Set("Upgrade", "websocket")
	req.Header.Set("X-Keep", "yes")
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)

	p := sender.wait(t)
	for _, banned := range []string{"Connection", "Proxy-Authorization", "Upgrade"} {
		if _, ok := p.Headers[banned]; ok {
			t.Errorf("%s should be stripped", banned)
		}
	}
	if p.Headers["X-Keep"] != "yes" {
		t.Error("X-Keep should be preserved")
	}
}

func TestCapture_XForwardedProtoSetsScheme(t *testing.T) {
	sender := newRecordingSender()
	h := New(sender, nil, 1024, time.Second, false)

	req := httptest.NewRequest(http.MethodGet, "/path?a=b", nil)
	req.Host = "svc.example"
	req.Header.Set("X-Forwarded-Proto", "https")
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)

	p := sender.wait(t)
	if p.URI != "https://svc.example/path?a=b" {
		t.Errorf("URI = %q, want https://svc.example/path?a=b", p.URI)
	}
}

func TestCapture_MultiValueHeaderLastWins(t *testing.T) {
	sender := newRecordingSender()
	h := New(sender, nil, 1024, time.Second, false)

	req := httptest.NewRequest(http.MethodGet, "http://svc.example/x", nil)
	req.Header.Add("X-Multi", "first")
	req.Header.Add("X-Multi", "last")
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)

	p := sender.wait(t)
	if p.Headers["X-Multi"] != "last" {
		t.Errorf("X-Multi = %q, want last", p.Headers["X-Multi"])
	}
}

func TestCapture_GoroutinePanicRecovered(t *testing.T) {
	sender := newRecordingSender()
	sender.panic = true
	h := New(sender, nil, 1024, time.Second, false)

	req := httptest.NewRequest(http.MethodGet, "http://svc.example/x", nil)
	rec := httptest.NewRecorder()

	// Must not panic the handler / test process.
	h.ServeHTTP(rec, req)
	if rec.Code != http.StatusNoContent {
		t.Errorf("status = %d, want 204", rec.Code)
	}
	// Give the goroutine a moment to run its deferred recover.
	time.Sleep(50 * time.Millisecond)
}
