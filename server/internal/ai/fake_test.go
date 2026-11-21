package ai

import (
	"context"
	"strings"
	"sync"

	"hackathon2026/server/internal/llm"
)

// fakeLLM answers by purpose: the first matching rule (substring of the last message, or of the
// system prompt) wins; replies are consumed in order per rule.
type fakeLLM struct {
	mu    sync.Mutex
	seat  []string
	crit  []string
	json  []string
	err   error
	calls []llm.CompletionReq
}

func pop(q *[]string) string {
	if len(*q) == 0 {
		return ""
	}
	s := (*q)[0]
	*q = (*q)[1:]
	return s
}

func (f *fakeLLM) Complete(ctx context.Context, req llm.CompletionReq) (llm.CompletionResp, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.calls = append(f.calls, req)
	if f.err != nil {
		return llm.CompletionResp{}, f.err
	}
	if err := ctx.Err(); err != nil {
		return llm.CompletionResp{}, err
	}
	var text string
	all := ""
	for _, m := range req.Messages {
		all += m.Content
	}
	switch {
	case req.JSON:
		text = pop(&f.json)
	case strings.Contains(all, "copy editor") || strings.Contains(all, "You edit chat messages"):
		text = pop(&f.crit)
	default:
		text = pop(&f.seat)
	}
	return llm.CompletionResp{Text: text, Provider: "fake", InputTokens: 10, OutputTokens: 5, Cost: 0.0001}, nil
}

func (f *fakeLLM) n() int { f.mu.Lock(); defer f.mu.Unlock(); return len(f.calls) }

var testPersona = Persona{Age: 34, Location: "Leipzig, DE", Job: "warehouse shift lead", Language: "en", NativeLanguage: "de",
	Typing:    Typing{Case: "lower", Punctuation: "none", TypoRate: 0, Emoji: "never", AvgLen: 40},
	Knowledge: "average", Mood: "bored", Quirks: []string{"uses 'tbh'"}, SkipRate: 0.08}
