// Package ai holds the seat and critic pipeline for AI players.
package ai

import (
	"encoding/json"
	"errors"
	"fmt"
	"slices"
	"strings"
)

type Typing struct {
	Case        string  `json:"case"`        // lower|mixed|proper
	Punctuation string  `json:"punctuation"` // none|light|full
	TypoRate    float64 `json:"typo_rate"`
	Emoji       string  `json:"emoji"` // never|rare|often
	AvgLen      int     `json:"avg_len"`
}

type Persona struct {
	Age            int      `json:"age"`
	Location       string   `json:"location"`
	Job            string   `json:"job"`
	Language       string   `json:"language"`
	NativeLanguage string   `json:"native_language"`
	Typing         Typing   `json:"typing"`
	Knowledge      string   `json:"knowledge"`
	Mood           string   `json:"mood"`
	Quirks         []string `json:"quirks"`
	SkipRate       float64  `json:"skip_rate"`
	// Optional (v2 persona generator): why this person is at the venue, and a short
	// description of how they talk. Empty for hand-written v1 personas.
	Reason string `json:"reason,omitempty"`
	Voice  string `json:"voice,omitempty"`
}

// String is the raw JSON sheet (v1 prompts embed it as-is).
func (p Persona) String() string {
	b, _ := json.Marshal(p)
	return string(b)
}

// Describe renders the persona as prose for v2 prompts. It deliberately leaves out the numeric
// knobs (typo_rate, skip_rate, avg_len): code applies those, and a model that sees
// "typo_rate: 0.04" tends to caricature it or leak it.
func (p Persona) Describe() string {
	var b strings.Builder
	fmt.Fprintf(&b, "You are %d, %s, from %s.", p.Age, p.Job, p.Location)
	if p.Reason != "" {
		fmt.Fprintf(&b, " You're here because %s.", strings.TrimSuffix(p.Reason, "."))
	}
	if p.NativeLanguage != "" && p.Language != "" && p.NativeLanguage != p.Language {
		fmt.Fprintf(&b, " You write in %s, but your first language is %s, so now and then a word order or phrase is slightly non-native (never broken).", langName(p.Language), langName(p.NativeLanguage))
	}
	fmt.Fprintf(&b, " Mood right now: %s.", p.Mood)
	switch {
	case p.Knowledge == "low":
		b.WriteString(" General knowledge: below average, not into trivia.")
	case strings.HasPrefix(p.Knowledge, "nerdy-about:"):
		fmt.Fprintf(&b, " General knowledge: average, but genuinely nerdy about %s.", strings.TrimPrefix(p.Knowledge, "nerdy-about:"))
	default:
		b.WriteString(" General knowledge: average.")
	}
	if p.Voice != "" {
		fmt.Fprintf(&b, " How you talk: %s.", strings.TrimSuffix(p.Voice, "."))
	}
	b.WriteString(" Typing: ")
	switch p.Typing.Case {
	case "lower":
		b.WriteString("all lowercase")
	case "proper":
		b.WriteString("normal capitalisation")
	default:
		b.WriteString("inconsistent capitalisation")
	}
	switch p.Typing.Punctuation {
	case "none":
		b.WriteString(", almost no punctuation")
	case "light":
		b.WriteString(", light punctuation")
	default:
		b.WriteString(", full sentences with punctuation")
	}
	b.WriteString(".")
	return b.String()
}

func langName(code string) string {
	switch strings.ToLower(code) {
	case "en":
		return "English"
	case "de":
		return "German"
	case "fr":
		return "French"
	case "es":
		return "Spanish"
	case "it":
		return "Italian"
	case "pl":
		return "Polish"
	case "uk":
		return "Ukrainian"
	case "pt":
		return "Portuguese"
	case "nl":
		return "Dutch"
	case "tr":
		return "Turkish"
	}
	return code
}

// Validate checks enums and ranges (used on director/persona-generator output).
func (p Persona) Validate() error {
	var errs []string
	if p.Age < 16 || p.Age > 90 {
		errs = append(errs, "age out of range")
	}
	if strings.TrimSpace(p.Job) == "" || strings.TrimSpace(p.Location) == "" {
		errs = append(errs, "job and location required")
	}
	if !oneOf(p.Typing.Case, "lower", "mixed", "proper") {
		errs = append(errs, "typing.case")
	}
	if !oneOf(p.Typing.Punctuation, "none", "light", "full") {
		errs = append(errs, "typing.punctuation")
	}
	if !oneOf(p.Typing.Emoji, "never", "rare", "often") {
		errs = append(errs, "typing.emoji")
	}
	if p.Typing.TypoRate < 0 || p.Typing.TypoRate > 0.15 {
		errs = append(errs, "typing.typo_rate")
	}
	if p.Typing.AvgLen < 10 || p.Typing.AvgLen > 180 {
		errs = append(errs, "typing.avg_len")
	}
	if !(oneOf(p.Knowledge, "average", "low") || strings.HasPrefix(p.Knowledge, "nerdy-about:")) {
		errs = append(errs, "knowledge")
	}
	if !oneOf(p.Mood, "bored", "friendly", "sarcastic", "distracted") {
		errs = append(errs, "mood")
	}
	if p.SkipRate < 0 || p.SkipRate > 0.3 {
		errs = append(errs, "skip_rate")
	}
	if len(p.Quirks) > 4 {
		errs = append(errs, "too many quirks")
	}
	if len(errs) > 0 {
		return errors.New("persona: " + strings.Join(errs, ", "))
	}
	return nil
}

func oneOf(s string, opts ...string) bool { return slices.Contains(opts, s) }
