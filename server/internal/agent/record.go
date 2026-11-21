package agent

import "time"

// CallRecord is one agent call, persisted to ai_calls (invariant 4). For the stub, Provider and
// ModelID are "stub" and there are no tokens.
type CallRecord struct {
	GameID        string
	Seat          int
	Purpose       string // intro|ask|answer|vote
	Provider      string
	ModelID       string
	PromptVersion string
	Final         string // JSON of the resolved reply
	LatencyMs     int
	Error         string
	CreatedAt     time.Time
}
