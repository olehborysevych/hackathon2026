package agent

import (
	"encoding/json"
	"time"
)

// CallRecord is one AI call, persisted to ai_calls (invariant 4). For the stub, Provider and
// ModelID are "stub" and there are no tokens; the LLM agent returns one record per LLM call
// (seat draft, echo retry, critic, vote) in Reply.Calls.
type CallRecord struct {
	GameID        string
	Seat          int
	Purpose       string // intro|ask|answer|critic|vote
	Provider      string // the provider that actually served the call (OpenRouter may fall back)
	ModelID       string
	PromptVersion string
	Request       json.RawMessage // model, temperature and messages sent
	Draft         string
	Final         string // stub: JSON of the resolved reply; LLM: the text after the pipeline
	LatencyMs     int
	InputTokens   int
	OutputTokens  int
	CostUSD       float64
	Error         string
	CreatedAt     time.Time
}
