package capture

import (
	"errors"
	"io"
	"net/http"
	"strings"
	"testing"
)

func newReq(body io.ReadCloser, contentLength int64) *http.Request {
	return &http.Request{Body: body, ContentLength: contentLength}
}

func TestReadBody_EmptyNoBody(t *testing.T) {
	r := newReq(http.NoBody, 0)
	data, trunc, err := ReadBody(r, 1024)
	if err != nil || trunc || data != nil {
		t.Fatalf("got data=%v trunc=%v err=%v, want nil/false/nil", data, trunc, err)
	}
}

func TestReadBody_NilBody(t *testing.T) {
	r := newReq(nil, 0)
	data, trunc, err := ReadBody(r, 1024)
	if err != nil || trunc || data != nil {
		t.Fatalf("got data=%v trunc=%v err=%v", data, trunc, err)
	}
}

func TestReadBody_ContentLengthZeroFastPath(t *testing.T) {
	r := newReq(io.NopCloser(strings.NewReader("ignored")), 0)
	data, _, err := ReadBody(r, 1024)
	if err != nil || data != nil {
		t.Fatalf("expected fast path nil, got data=%v err=%v", data, err)
	}
}

func TestReadBody_ExactlyAtLimit(t *testing.T) {
	r := newReq(io.NopCloser(strings.NewReader("12345")), 5)
	data, trunc, err := ReadBody(r, 5)
	if err != nil {
		t.Fatal(err)
	}
	if trunc {
		t.Error("should not be truncated at exact limit")
	}
	if string(data) != "12345" {
		t.Errorf("got %q", data)
	}
}

func TestReadBody_ExceedsLimitTruncates(t *testing.T) {
	r := newReq(io.NopCloser(strings.NewReader("1234567890")), 10)
	data, trunc, err := ReadBody(r, 4)
	if err != nil {
		t.Fatal(err)
	}
	if !trunc {
		t.Error("expected truncated=true")
	}
	if string(data) != "1234" {
		t.Errorf("got %q, want 1234", data)
	}
}

func TestReadBody_ShorterThanLimit(t *testing.T) {
	r := newReq(io.NopCloser(strings.NewReader("ab")), 2)
	data, trunc, err := ReadBody(r, 100)
	if err != nil || trunc || string(data) != "ab" {
		t.Fatalf("got data=%q trunc=%v err=%v", data, trunc, err)
	}
}

type errReader struct {
	data []byte
	pos  int
}

func (e *errReader) Read(p []byte) (int, error) {
	if e.pos < len(e.data) {
		n := copy(p, e.data[e.pos:])
		e.pos += n
		return n, nil
	}
	return 0, errors.New("boom")
}
func (e *errReader) Close() error { return nil }

func TestReadBody_ReadErrorReturnsPartial(t *testing.T) {
	r := newReq(&errReader{data: []byte("partial")}, 7)
	data, _, err := ReadBody(r, 1024)
	if err == nil {
		t.Fatal("expected read error")
	}
	if string(data) != "partial" {
		t.Errorf("expected partial bytes, got %q", data)
	}
}

func TestReadFullBody_EmptyFastPath(t *testing.T) {
	r := newReq(http.NoBody, 0)
	full, capture, trunc, err := ReadFullBody(r, 1024)
	if err != nil || trunc || full != nil || capture != nil {
		t.Fatalf("got full=%v capture=%v trunc=%v err=%v", full, capture, trunc, err)
	}
}

func TestReadFullBody_TruncatesCaptureButKeepsFull(t *testing.T) {
	r := newReq(io.NopCloser(strings.NewReader("1234567890")), 10)
	full, capture, trunc, err := ReadFullBody(r, 4)
	if err != nil {
		t.Fatal(err)
	}
	if !trunc {
		t.Error("expected truncated=true")
	}
	if string(full) != "1234567890" {
		t.Errorf("full = %q, want full body", full)
	}
	if string(capture) != "1234" {
		t.Errorf("capture = %q, want 1234", capture)
	}
}
