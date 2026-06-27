// Package config parses and validates all SnapReq configuration from the
// environment exactly once at startup. See Agent.md §4.
package config

import (
	"log"
	"log/slog"
	"os"
	"strconv"
	"time"
)

// Config holds all resolved SnapReq configuration. It is constructed once via
// Load and owned by main; no package keeps global mutable copies.
type Config struct {
	EchoChamberURL     string
	EchoChamberToken   string // never logged at any level
	ListenAddr         string
	ForwardURL         string // empty => Mode B/D (mirror); set => Mode A (proxy)
	ForwardTimeout     time.Duration
	CaptureTimeout     time.Duration
	MaxBodyBytes       int64
	LogLevel           string
	CaptureAuthHeaders bool
}

// IsProxyMode reports whether SnapReq runs as an in-path proxy (Mode A).
func (c Config) IsProxyMode() bool { return c.ForwardURL != "" }

// SlogLevel maps the configured LOG_LEVEL string to a slog.Level.
func (c Config) SlogLevel() slog.Level {
	switch c.LogLevel {
	case "debug":
		return slog.LevelDebug
	case "warn":
		return slog.LevelWarn
	case "error":
		return slog.LevelError
	default:
		return slog.LevelInfo
	}
}

// LogAttrs returns the resolved configuration as slog attributes, deliberately
// omitting EchoChamberToken so it can never be logged.
func (c Config) LogAttrs() []any {
	mode := "mirror"
	if c.IsProxyMode() {
		mode = "proxy"
	}
	return []any{
		slog.String("mode", mode),
		slog.String("echochamber_url", c.EchoChamberURL),
		slog.Bool("ingest_auth", c.EchoChamberToken != ""),
		slog.String("listen_addr", c.ListenAddr),
		slog.String("forward_url", c.ForwardURL),
		slog.Duration("forward_timeout", c.ForwardTimeout),
		slog.Duration("capture_timeout", c.CaptureTimeout),
		slog.Int64("max_body_bytes", c.MaxBodyBytes),
		slog.String("log_level", c.LogLevel),
		slog.Bool("capture_auth_headers", c.CaptureAuthHeaders),
	}
}

// Load reads, validates, and returns the configuration. Missing required values
// or malformed numeric values are fatal — SnapReq must not start misconfigured.
func Load() Config {
	c := Config{
		EchoChamberURL:     os.Getenv("ECHOCHAMBER_URL"),
		EchoChamberToken:   os.Getenv("ECHOCHAMBER_TOKEN"),
		ListenAddr:         getEnv("LISTEN_ADDR", ":8080"),
		ForwardURL:         os.Getenv("FORWARD_URL"),
		ForwardTimeout:     getMillis("FORWARD_TIMEOUT_MS", 5000),
		CaptureTimeout:     getMillis("CAPTURE_TIMEOUT_MS", 2000),
		MaxBodyBytes:       getInt64("MAX_BODY_BYTES", 1048576),
		LogLevel:           getEnv("LOG_LEVEL", "info"),
		CaptureAuthHeaders: getBool("CAPTURE_AUTH_HEADERS"),
	}

	if c.EchoChamberURL == "" {
		log.Fatal("ECHOCHAMBER_URL is required")
	}
	// ECHOCHAMBER_TOKEN is optional: when empty, ingest is sent without an
	// Authorization header (EchoChamber must also have ingest auth disabled).

	return c
}

func getEnv(key, def string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return def
}

func getInt64(key string, def int64) int64 {
	v := os.Getenv(key)
	if v == "" {
		return def
	}
	n, err := strconv.ParseInt(v, 10, 64)
	if err != nil {
		log.Fatalf("%s must be an integer, got %q", key, v)
	}
	return n
}

func getMillis(key string, defMS int64) time.Duration {
	return time.Duration(getInt64(key, defMS)) * time.Millisecond
}

func getBool(key string) bool {
	switch os.Getenv(key) {
	case "true", "1":
		return true
	default:
		return false
	}
}
