package forward

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

func TestForward_PreservesMethodPathQueryAndStripsHopByHop(t *testing.T) {
	var gotMethod, gotURI, gotKeep, gotConn, gotBody string
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotMethod = r.Method
		gotURI = r.URL.RequestURI()
		gotKeep = r.Header.Get("X-Keep")
		gotConn = r.Header.Get("Connection")
		b, _ := io.ReadAll(r.Body)
		gotBody = string(b)
		w.WriteHeader(http.StatusTeapot)
	}))
	defer upstream.Close()

	c := NewClient(upstream.URL, time.Second)

	in := httptest.NewRequest(http.MethodPut, "/things?x=1&y=2", strings.NewReader("payload"))
	in.Header.Set("X-Keep", "yes")
	in.Header.Set("Connection", "keep-alive")

	resp, err := c.Forward(context.Background(), in, []byte("payload"))
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusTeapot {
		t.Errorf("status = %d", resp.StatusCode)
	}
	if gotMethod != http.MethodPut {
		t.Errorf("method = %q", gotMethod)
	}
	if gotURI != "/things?x=1&y=2" {
		t.Errorf("uri = %q", gotURI)
	}
	if gotKeep != "yes" {
		t.Error("X-Keep should be forwarded")
	}
	if gotConn != "" {
		t.Errorf("Connection (hop-by-hop) should be stripped, got %q", gotConn)
	}
	if gotBody != "payload" {
		t.Errorf("body = %q", gotBody)
	}
}

func TestForward_UpstreamErrorReturnsError(t *testing.T) {
	c := NewClient("http://127.0.0.1:1", time.Second) // nothing listening
	in := httptest.NewRequest(http.MethodGet, "/x", nil)
	if _, err := c.Forward(context.Background(), in, nil); err == nil {
		t.Fatal("expected error on upstream failure")
	}
}
