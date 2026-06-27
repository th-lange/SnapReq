// Command snapreq is the SnapReq capture sidecar entrypoint. It wires config,
// the shared HTTP clients, the capture handler, and the server. All logic lives
// in internal packages; main only wires and fatals (Agent.md §2).
package main

import (
	"context"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/th-lange/snapreq/internal/capture"
	"github.com/th-lange/snapreq/internal/config"
	"github.com/th-lange/snapreq/internal/forward"
	"github.com/th-lange/snapreq/internal/ingest"
)

func main() {
	cfg := config.Load()

	slog.SetDefault(slog.New(slog.NewTextHandler(os.Stdout, &slog.HandlerOptions{
		Level: cfg.SlogLevel(),
	})))
	slog.Info("starting snapreq", cfg.LogAttrs()...)

	srv := &http.Server{
		Addr:    cfg.ListenAddr,
		Handler: newHandler(cfg),
	}

	runServer(srv)
}

// newHandler wires the ingest client, the optional forward client (Mode A), and
// the capture handler from resolved config.
func newHandler(cfg config.Config) http.Handler {
	ingestClient := ingest.NewClient(cfg.EchoChamberURL, cfg.EchoChamberToken, cfg.CaptureTimeout)

	var forwarder capture.Forwarder
	if cfg.IsProxyMode() {
		forwarder = forward.NewClient(cfg.ForwardURL, cfg.ForwardTimeout)
	}

	return capture.New(ingestClient, forwarder, cfg.MaxBodyBytes, cfg.CaptureTimeout, cfg.CaptureAuthHeaders)
}

// runServer starts the HTTP server and blocks until SIGINT/SIGTERM, then shuts
// down gracefully with a bounded timeout.
func runServer(srv *http.Server) {
	idleClosed := make(chan struct{})
	go func() {
		sigCh := make(chan os.Signal, 1)
		signal.Notify(sigCh, syscall.SIGINT, syscall.SIGTERM)
		<-sigCh

		slog.Info("shutdown signal received, draining")
		ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		if err := srv.Shutdown(ctx); err != nil {
			slog.Error("graceful shutdown failed", slog.String("err", err.Error()))
		}
		close(idleClosed)
	}()

	slog.Info("listening", slog.String("addr", srv.Addr))
	if err := srv.ListenAndServe(); err != nil && err != http.ErrServerClosed {
		slog.Error("server error", slog.String("err", err.Error()))
		os.Exit(1)
	}
	<-idleClosed
	slog.Info("snapreq stopped")
}
