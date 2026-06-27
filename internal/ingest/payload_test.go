package ingest

import (
	"encoding/json"
	"testing"
)

func TestIngestPayload_SerialisesCanonicalShape(t *testing.T) {
	body := "hello"
	p := IngestPayload{
		Method:    "POST",
		URI:       "http://example.com/v1/things?x=1",
		Authority: "example.com",
		Headers:   map[string]string{"Content-Type": "application/json"},
		Body:      &body,
	}

	got, err := json.Marshal(p)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}

	want := `{"method":"POST","uri":"http://example.com/v1/things?x=1","authority":"example.com","headers":{"Content-Type":"application/json"},"body":"hello"}`
	if string(got) != want {
		t.Errorf("unexpected JSON\n got: %s\nwant: %s", got, want)
	}
}

func TestIngestPayload_NilBodySerialisesAsNull(t *testing.T) {
	p := IngestPayload{Method: "GET", Headers: map[string]string{}}
	got, err := json.Marshal(p)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	want := `{"method":"GET","uri":"","authority":"","headers":{},"body":null}`
	if string(got) != want {
		t.Errorf("unexpected JSON\n got: %s\nwant: %s", got, want)
	}
}

func TestIngestPayload_EmptyHeadersSerialiseAsObjectNotNull(t *testing.T) {
	p := IngestPayload{Headers: map[string]string{}}
	got, err := json.Marshal(p)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	if want := `"headers":{}`; !contains(string(got), want) {
		t.Errorf("expected %s in %s", want, got)
	}
}

func TestIngestPayload_DeserialiseNullBody(t *testing.T) {
	var p IngestPayload
	if err := json.Unmarshal([]byte(`{"method":"GET","body":null}`), &p); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if p.Body != nil {
		t.Errorf("expected nil Body, got %v", *p.Body)
	}
}

func TestIngestPayload_DeserialiseStringBody(t *testing.T) {
	var p IngestPayload
	if err := json.Unmarshal([]byte(`{"method":"POST","body":"payload"}`), &p); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if p.Body == nil {
		t.Fatal("expected non-nil Body")
	}
	if *p.Body != "payload" {
		t.Errorf("got %q, want %q", *p.Body, "payload")
	}
}

func contains(s, sub string) bool {
	for i := 0; i+len(sub) <= len(s); i++ {
		if s[i:i+len(sub)] == sub {
			return true
		}
	}
	return false
}
