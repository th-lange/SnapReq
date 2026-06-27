package config

import (
	"os"
	"os/exec"
	"testing"
	"time"
)

func TestLoad_DefaultsApplied(t *testing.T) {
	t.Setenv("ECHOCHAMBER_URL", "http://ec:8080")
	t.Setenv("ECHOCHAMBER_TOKEN", "tok")
	for _, k := range []string{"LISTEN_ADDR", "FORWARD_URL", "FORWARD_TIMEOUT_MS", "CAPTURE_TIMEOUT_MS", "MAX_BODY_BYTES", "LOG_LEVEL", "CAPTURE_AUTH_HEADERS"} {
		t.Setenv(k, "")
	}

	c := Load()

	if c.ListenAddr != ":8080" {
		t.Errorf("ListenAddr = %q, want :8080", c.ListenAddr)
	}
	if c.ForwardTimeout != 5000*time.Millisecond {
		t.Errorf("ForwardTimeout = %v, want 5s", c.ForwardTimeout)
	}
	if c.CaptureTimeout != 2000*time.Millisecond {
		t.Errorf("CaptureTimeout = %v, want 2s", c.CaptureTimeout)
	}
	if c.MaxBodyBytes != 1048576 {
		t.Errorf("MaxBodyBytes = %d, want 1048576", c.MaxBodyBytes)
	}
	if c.LogLevel != "info" {
		t.Errorf("LogLevel = %q, want info", c.LogLevel)
	}
	if c.CaptureAuthHeaders {
		t.Error("CaptureAuthHeaders should default false")
	}
	if c.IsProxyMode() {
		t.Error("should be mirror mode with no FORWARD_URL")
	}
}

func TestLoad_CaptureAuthHeaders(t *testing.T) {
	t.Setenv("ECHOCHAMBER_URL", "http://ec:8080")
	t.Setenv("ECHOCHAMBER_TOKEN", "tok")

	for _, v := range []string{"true", "1"} {
		t.Setenv("CAPTURE_AUTH_HEADERS", v)
		if !Load().CaptureAuthHeaders {
			t.Errorf("CAPTURE_AUTH_HEADERS=%q should parse true", v)
		}
	}
	t.Setenv("CAPTURE_AUTH_HEADERS", "")
	if Load().CaptureAuthHeaders {
		t.Error("absent CAPTURE_AUTH_HEADERS should be false")
	}
}

func TestLoad_ProxyModeWhenForwardURLSet(t *testing.T) {
	t.Setenv("ECHOCHAMBER_URL", "http://ec:8080")
	t.Setenv("ECHOCHAMBER_TOKEN", "tok")
	t.Setenv("FORWARD_URL", "http://upstream:9000")

	c := Load()
	if !c.IsProxyMode() {
		t.Error("expected proxy mode")
	}
}

// The following tests verify log.Fatal paths via a re-exec sub-process: the test
// binary runs the relevant Load() in a child process and the parent asserts a
// non-zero exit.

func TestLoad_MissingURLExits(t *testing.T) {
	if os.Getenv("GO_TEST_FATAL") == "url" {
		os.Unsetenv("ECHOCHAMBER_URL")
		os.Setenv("ECHOCHAMBER_TOKEN", "tok")
		Load()
		return
	}
	assertFatal(t, "TestLoad_MissingURLExits", "url")
}

func TestLoad_MissingTokenExits(t *testing.T) {
	if os.Getenv("GO_TEST_FATAL") == "token" {
		os.Setenv("ECHOCHAMBER_URL", "http://ec:8080")
		os.Unsetenv("ECHOCHAMBER_TOKEN")
		Load()
		return
	}
	assertFatal(t, "TestLoad_MissingTokenExits", "token")
}

func TestLoad_InvalidMaxBodyBytesExits(t *testing.T) {
	if os.Getenv("GO_TEST_FATAL") == "maxbody" {
		os.Setenv("ECHOCHAMBER_URL", "http://ec:8080")
		os.Setenv("ECHOCHAMBER_TOKEN", "tok")
		os.Setenv("MAX_BODY_BYTES", "not-a-number")
		Load()
		return
	}
	assertFatal(t, "TestLoad_InvalidMaxBodyBytesExits", "maxbody")
}

func assertFatal(t *testing.T, name, mode string) {
	t.Helper()
	cmd := exec.Command(os.Args[0], "-test.run=^"+name+"$")
	cmd.Env = append(os.Environ(), "GO_TEST_FATAL="+mode)
	err := cmd.Run()
	if ee, ok := err.(*exec.ExitError); !ok || ee.Success() {
		t.Fatalf("expected non-zero exit, got err=%v", err)
	}
}
