// Package llm defines the LLM interface and the openai_compat adapter (OpenRouter by default).
package llm

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"
)

type Message struct {
	Role    string `json:"role"` // system|user|assistant
	Content string `json:"content"`
}

type CompletionReq struct {
	Model       string
	Messages    []Message
	Temperature float64
	MaxTokens   int
	JSON        bool // ask for a JSON object
	// ReasoningEffort: "" sends reasoning disabled (seat/critic: short replies, hidden thinking
	// eats the budget); "low"/"medium"/"high" for models where reasoning is mandatory (judge).
	ReasoningEffort string
}

type CompletionResp struct {
	Text         string
	Model        string
	Provider     string // actual upstream provider reported by OpenRouter
	InputTokens  int
	OutputTokens int
	Cost         float64 // USD, as reported by OpenRouter (0 if the endpoint doesn't report it)
	Latency      time.Duration
}

type LLM interface {
	Complete(ctx context.Context, req CompletionReq) (CompletionResp, error)
}

// OpenAICompat talks to any OpenAI-compatible /chat/completions endpoint.
type OpenAICompat struct {
	BaseURL string
	APIKey  string
	HTTP    *http.Client
}

func NewOpenAICompat(baseURL, apiKey string) *OpenAICompat {
	if baseURL == "" {
		baseURL = "https://openrouter.ai/api/v1"
	}
	return &OpenAICompat{BaseURL: strings.TrimRight(baseURL, "/"), APIKey: apiKey, HTTP: &http.Client{}}
}

func (c *OpenAICompat) Complete(ctx context.Context, req CompletionReq) (CompletionResp, error) {
	body := map[string]any{
		"model":       req.Model,
		"messages":    req.Messages,
		"temperature": req.Temperature,
		// Reasoning off by default: short chat replies, and hidden thinking eats the token budget.
		"reasoning": map[string]any{"enabled": false},
		// OpenRouter returns the actual cost in usage.cost; other endpoints ignore this.
		"usage": map[string]any{"include": true},
	}
	if req.ReasoningEffort != "" {
		body["reasoning"] = map[string]any{"effort": req.ReasoningEffort}
	}
	if req.MaxTokens > 0 {
		body["max_tokens"] = req.MaxTokens
	}
	if req.JSON {
		body["response_format"] = map[string]string{"type": "json_object"}
	}
	buf, _ := json.Marshal(body)
	hr, err := http.NewRequestWithContext(ctx, http.MethodPost, c.BaseURL+"/chat/completions", bytes.NewReader(buf))
	if err != nil {
		return CompletionResp{}, err
	}
	hr.Header.Set("Content-Type", "application/json")
	hr.Header.Set("Authorization", "Bearer "+c.APIKey)

	start := time.Now()
	res, err := c.HTTP.Do(hr)
	if err != nil {
		return CompletionResp{}, err
	}
	defer res.Body.Close()
	raw, _ := io.ReadAll(res.Body)
	lat := time.Since(start)
	if res.StatusCode != http.StatusOK {
		return CompletionResp{Latency: lat}, fmt.Errorf("llm: status %d: %s", res.StatusCode, truncate(string(raw), 300))
	}
	var out struct {
		Model    string `json:"model"`
		Provider string `json:"provider"`
		Choices  []struct {
			Message struct {
				Content string `json:"content"`
			} `json:"message"`
		} `json:"choices"`
		Usage struct {
			Prompt     int     `json:"prompt_tokens"`
			Completion int     `json:"completion_tokens"`
			Cost       float64 `json:"cost"`
		} `json:"usage"`
		Error *struct {
			Message string `json:"message"`
		} `json:"error"`
	}
	if err := json.Unmarshal(raw, &out); err != nil {
		return CompletionResp{Latency: lat}, fmt.Errorf("llm: decode: %w", err)
	}
	if out.Error != nil {
		return CompletionResp{Latency: lat}, fmt.Errorf("llm: %s", out.Error.Message)
	}
	if len(out.Choices) == 0 {
		return CompletionResp{Latency: lat}, fmt.Errorf("llm: no choices: %s", truncate(string(raw), 300))
	}
	return CompletionResp{
		Text:         strings.TrimSpace(out.Choices[0].Message.Content),
		Model:        out.Model,
		Provider:     out.Provider,
		InputTokens:  out.Usage.Prompt,
		OutputTokens: out.Usage.Completion,
		Cost:         out.Usage.Cost,
		Latency:      lat,
	}, nil
}

func truncate(s string, n int) string {
	if len(s) > n {
		return s[:n] + "…"
	}
	return s
}
