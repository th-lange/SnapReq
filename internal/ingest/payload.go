// Package ingest defines the shared SnapReq→EchoChamber contract and the
// fire-and-forget client that delivers captured requests.
package ingest

// IngestPayload is the JSON shape POSTed to EchoChamber's /internal/ingest.
// It is the source of truth for the contract on the SnapReq side and must stay
// in sync with EchoChamber's CapturedRequest DTO (Agent.md §3). Any change here
// requires a simultaneous change there.
//
// Note: capturedAt is intentionally absent — EchoChamber stamps it on receipt.
type IngestPayload struct {
	Method    string            `json:"method"`
	URI       string            `json:"uri"`       // full URI incl. scheme+host
	Authority string            `json:"authority"` // host:port
	Headers   map[string]string `json:"headers"`   // single value per name (last wins)
	Body      *string           `json:"body"`      // nil when no body / not captured
}
