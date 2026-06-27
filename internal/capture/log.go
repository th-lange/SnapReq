package capture

import (
	"context"
	"log/slog"
)

// LevelCritical is a severity above slog.LevelError, reserved for operational
// alerts that must never be confused with a failed client request. SnapReq uses
// it when a captured request is dropped — e.g. EchoChamber is unreachable. The
// inbound request always succeeds; this entry tells operators captures are being
// lost so they can act, without implying the request itself errored.
const LevelCritical = slog.Level(12)

// logCritical emits a CRITICAL-level entry on the default logger.
func logCritical(msg string, args ...any) {
	slog.Default().Log(context.Background(), LevelCritical, msg, args...)
}
