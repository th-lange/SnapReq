package ingest

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"
)

func TestClient_Send_PostsCorrectRequest(t *testing.T) {
	var gotPath, gotAuth, gotCT string
	var gotPayload IngestPayload

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotPath = r.URL.Path
		gotAuth = r.Header.Get("Authorization")
		gotCT = r.Header.Get("Content-Type")
		b, _ := io.ReadAll(r.Body)
		_ = json.Unmarshal(b, &gotPayload)
		w.WriteHeader(http.StatusAccepted)
	}))
	defer srv.Close()

	c := NewClient(srv.URL, "secret-token", time.Second)
	body := "x"
	err := c.Send(context.Background(), IngestPayload{Method: "PUT", Body: &body})
	if err != nil {
		t.Fatalf("Send: %v", err)
	}

	if gotPath != "/internal/ingest" {
		t.Errorf("path = %q, want /internal/ingest", gotPath)
	}
	if gotAuth != "Bearer secret-token" {
		t.Errorf("auth = %q, want Bearer secret-token", gotAuth)
	}
	if gotCT != "application/json" {
		t.Errorf("content-type = %q", gotCT)
	}
	if gotPayload.Method != "PUT" || gotPayload.Body == nil || *gotPayload.Body != "x" {
		t.Errorf("unexpected payload: %+v", gotPayload)
	}
}

func TestClient_Send_NoTokenOmitsAuthHeader(t *testing.T) {
	var hadAuth bool
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, hadAuth = r.Header["Authorization"]
		w.WriteHeader(http.StatusAccepted)
	}))
	defer srv.Close()

	c := NewClient(srv.URL, "", time.Second) // no token
	if err := c.Send(context.Background(), IngestPayload{Method: "GET"}); err != nil {
		t.Fatalf("Send: %v", err)
	}
	if hadAuth {
		t.Error("Authorization header should be omitted when no token is set")
	}
}

func TestClient_Send_Non2xxIsError(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
	}))
	defer srv.Close()

	c := NewClient(srv.URL, "t", time.Second)
	if err := c.Send(context.Background(), IngestPayload{}); err == nil {
		t.Fatal("expected error on non-2xx, got nil")
	}
}

func TestClient_Send_CancelledContextReturnsPromptly(t *testing.T) {
	block := make(chan struct{})
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		<-block // never respond until test ends
	}))
	defer srv.Close()
	defer close(block)

	c := NewClient(srv.URL, "t", 5*time.Second)
	ctx, cancel := context.WithCancel(context.Background())
	cancel() // already cancelled

	done := make(chan error, 1)
	go func() { done <- c.Send(ctx, IngestPayload{}) }()

	select {
	case err := <-done:
		if err == nil {
			t.Fatal("expected error from cancelled context")
		}
	case <-time.After(time.Second):
		t.Fatal("Send did not return promptly on cancelled context")
	}
}
