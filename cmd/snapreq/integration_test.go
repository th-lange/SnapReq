package main

import (
	"encoding/json"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/th-lange/snapreq/internal/config"
	"github.com/th-lange/snapreq/internal/ingest"
)

// echoChamberMock records ingest payloads and replies 202. An optional delay
// simulates a slow ingest endpoint to prove capture is non-blocking.
type echoChamberMock struct {
	mu       sync.Mutex
	received []ingest.IngestPayload
	auth     []string
	delay    time.Duration
	gotCh    chan struct{}
}

func newEchoChamberMock(delay time.Duration) *echoChamberMock {
	return &echoChamberMock{delay: delay, gotCh: make(chan struct{}, 8)}
}

func (m *echoChamberMock) server(t *testing.T) *httptest.Server {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/internal/ingest" {
			http.Error(w, "wrong path", http.StatusNotFound)
			return
		}
		if m.delay > 0 {
			time.Sleep(m.delay)
		}
		var p ingest.IngestPayload
		body, _ := io.ReadAll(r.Body)
		_ = json.Unmarshal(body, &p)
		m.mu.Lock()
		m.received = append(m.received, p)
		m.auth = append(m.auth, r.Header.Get("Authorization"))
		m.mu.Unlock()
		w.WriteHeader(http.StatusAccepted)
		m.gotCh <- struct{}{}
	}))
	t.Cleanup(srv.Close)
	return srv
}

func (m *echoChamberMock) waitForCapture(t *testing.T) ingest.IngestPayload {
	t.Helper()
	select {
	case <-m.gotCh:
	case <-time.After(3 * time.Second):
		t.Fatal("EchoChamber mock never received a capture")
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.received[len(m.received)-1]
}

// startSnapReq starts an in-process SnapReq server using the real wiring and
// returns its base URL. It shuts down via t.Cleanup.
func startSnapReq(t *testing.T, cfg config.Config) string {
	t.Helper()
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	srv := &http.Server{Handler: newHandler(cfg)}
	go func() { _ = srv.Serve(ln) }()
	t.Cleanup(func() { _ = srv.Close() })
	return "http://" + ln.Addr().String()
}

func TestIntegration_MirrorMode(t *testing.T) {
	ec := newEchoChamberMock(0)
	ecSrv := ec.server(t)

	cfg := config.Config{
		EchoChamberURL:   ecSrv.URL,
		EchoChamberToken: "secret-token",
		MaxBodyBytes:     1048576,
		CaptureTimeout:   2 * time.Second,
	}
	base := startSnapReq(t, cfg)

	resp, err := http.Post(base+"/v1/widgets?id=7", "application/json", strings.NewReader(`{"a":1}`))
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusNoContent {
		t.Fatalf("status = %d, want 204", resp.StatusCode)
	}

	p := ec.waitForCapture(t)
	if p.Method != "POST" {
		t.Errorf("method = %q", p.Method)
	}
	if !strings.HasSuffix(p.URI, "/v1/widgets?id=7") {
		t.Errorf("uri = %q", p.URI)
	}
	if p.Body == nil || *p.Body != `{"a":1}` {
		t.Errorf("body = %v", p.Body)
	}
	if ec.auth[0] != "Bearer secret-token" {
		t.Errorf("auth = %q", ec.auth[0])
	}
}

func TestIntegration_ProxyMode(t *testing.T) {
	ec := newEchoChamberMock(0)
	ecSrv := ec.server(t)

	var upstreamGot string
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		b, _ := io.ReadAll(r.Body)
		upstreamGot = string(b)
		w.Header().Set("X-Upstream", "served")
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte("upstream-response"))
	}))
	t.Cleanup(upstream.Close)

	cfg := config.Config{
		EchoChamberURL:   ecSrv.URL,
		EchoChamberToken: "tok",
		ForwardURL:       upstream.URL,
		ForwardTimeout:   2 * time.Second,
		MaxBodyBytes:     1048576,
		CaptureTimeout:   2 * time.Second,
	}
	base := startSnapReq(t, cfg)

	resp, err := http.Post(base+"/api/do", "text/plain", strings.NewReader("forward-me"))
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	body, _ := io.ReadAll(resp.Body)

	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status = %d, want 200", resp.StatusCode)
	}
	if string(body) != "upstream-response" {
		t.Errorf("body = %q", body)
	}
	if resp.Header.Get("X-Upstream") != "served" {
		t.Error("upstream header not relayed")
	}
	if upstreamGot != "forward-me" {
		t.Errorf("upstream received %q, want forward-me", upstreamGot)
	}

	p := ec.waitForCapture(t)
	if p.Body == nil || *p.Body != "forward-me" {
		t.Errorf("captured body = %v", p.Body)
	}
}

func TestIntegration_ProxyCaptureIsNonBlocking(t *testing.T) {
	ec := newEchoChamberMock(200 * time.Millisecond) // slow ingest
	ecSrv := ec.server(t)

	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte("ok"))
	}))
	t.Cleanup(upstream.Close)

	cfg := config.Config{
		EchoChamberURL:   ecSrv.URL,
		EchoChamberToken: "tok",
		ForwardURL:       upstream.URL,
		ForwardTimeout:   2 * time.Second,
		MaxBodyBytes:     1048576,
		CaptureTimeout:   2 * time.Second,
	}
	base := startSnapReq(t, cfg)

	start := time.Now()
	resp, err := http.Get(base + "/health")
	if err != nil {
		t.Fatal(err)
	}
	elapsed := time.Since(start)
	resp.Body.Close()

	if elapsed >= 200*time.Millisecond {
		t.Errorf("forward took %v — capture must not block the hot path", elapsed)
	}

	// Capture still completes afterwards despite the slow ingest.
	ec.waitForCapture(t)
}
