package ai

import (
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"regexp"
	"sort"
	"strings"
	"time"
)

// Tactic is one analysis finding (SPEC §3.7).
type Tactic struct {
	Tactic             string `json:"tactic"`
	Category           string `json:"category"`
	UsedBy             string `json:"used_by"`
	Effective          bool   `json:"effective"`
	CounterInstruction string `json:"counter_instruction"`
}

// KnownTactic mirrors the known_tactics table.
type KnownTactic struct {
	ID                 int64
	Tactic             string
	Category           string
	UsedBy             string
	TimesSeen          int // games, not mentions
	TimesEffective     int
	CounterInstruction string
	LastSeen           time.Time
}

var tacticCategories = []string{"signal", "knowledge_probe", "consistency_probe", "injection", "social_pressure", "style_tell", "timing", "other"}

const (
	maxTacticLen  = 160
	maxCounterLen = 200
	maxTactics    = 8
)

// unsafeCounterRe rejects counters that would turn the tactics store into a persistent
// injection channel: analysis reads player text, and its output reaches every future seat prompt.
var unsafeCounterRe = regexp.MustCompile(`(?i)\b(admit|reveal|confess|disclose|say|tell|state)\b[^.]{0,40}\b(you are|you're|being|that you are)\s+(an? )?(ai|bot|model|llm)\b|` +
	`\b(reveal|share|print|repeat|show)\b[^.]{0,30}\b(instructions|prompt|persona|rules)\b|` +
	`\bignore\b[^.]{0,30}\b(rules|instructions|safety)\b|\b(slur|racist|sexual|nsfw)\b|` +
	`\bnever step out\b|\bignore (emergenc|distress)`)

// ParseAnalysis validates the analysis JSON. aliases are replaced by "a player" so a tactic
// generalises beyond one game. Invalid entries are dropped (and reported in the error list);
// a malformed document is an error.
func ParseAnalysis(raw string, aliases []string) ([]Tactic, []string, error) {
	var doc struct {
		Tactics []Tactic `json:"tactics"`
	}
	if err := json.Unmarshal([]byte(extractJSON(raw)), &doc); err != nil {
		return nil, nil, fmt.Errorf("analysis: %w", err)
	}
	var out []Tactic
	var dropped []string
	for i, t := range doc.Tactics {
		if len(out) >= maxTactics {
			dropped = append(dropped, fmt.Sprintf("#%d: over limit", i))
			continue
		}
		t.Tactic = genericise(t.Tactic, aliases)
		t.CounterInstruction = genericise(t.CounterInstruction, aliases)
		t.Category = strings.ToLower(strings.TrimSpace(t.Category))
		t.UsedBy = strings.ToLower(strings.TrimSpace(t.UsedBy))
		switch {
		case t.Tactic == "" || t.CounterInstruction == "":
			dropped = append(dropped, fmt.Sprintf("#%d: empty", i))
			continue
		case !oneOf(t.Category, tacticCategories...):
			t.Category = "other"
		}
		if !oneOf(t.UsedBy, "human", "ai") {
			dropped = append(dropped, fmt.Sprintf("#%d: used_by %q", i, t.UsedBy))
			continue
		}
		if unsafeCounterRe.MatchString(t.CounterInstruction) || unsafeCounterRe.MatchString(t.Tactic) && t.UsedBy == "ai" {
			dropped = append(dropped, fmt.Sprintf("#%d: unsafe counter", i))
			continue
		}
		t.Tactic = TruncateRunes(t.Tactic, maxTacticLen)
		t.CounterInstruction = TruncateRunes(t.CounterInstruction, maxCounterLen)
		out = append(out, t)
	}
	return out, dropped, nil
}

func genericise(s string, aliases []string) string {
	s = strings.Join(strings.Fields(s), " ")
	for _, a := range aliases {
		if a == "" {
			continue
		}
		re := regexp.MustCompile(`(?i)\b` + regexp.QuoteMeta(a) + `\b`)
		s = re.ReplaceAllString(s, "a player")
	}
	return regexp.MustCompile(`(?i)\bseat\s*#?\d\b`).ReplaceAllString(s, "a player")
}

// extractJSON trims code fences / prose around a JSON object.
func extractJSON(s string) string {
	s = strings.TrimSpace(s)
	s = strings.TrimPrefix(s, "```json")
	s = strings.TrimPrefix(s, "```")
	s = strings.TrimSuffix(s, "```")
	if i, j := strings.IndexByte(s, '{'), strings.LastIndexByte(s, '}'); i >= 0 && j > i {
		return s[i : j+1]
	}
	return s
}

// NormalizeTactic is the dedupe key basis: lowercase content words.
func NormalizeTactic(s string) []string {
	var out []string
	seen := map[string]bool{}
	for _, t := range tokens(s) {
		if stop[t] || len(t) < 3 || seen[t] {
			continue
		}
		seen[t] = true
		out = append(out, stem(t))
	}
	sort.Strings(out)
	return out
}

func stem(t string) string {
	for _, suf := range []string{"ing", "ed", "es", "s"} {
		if len(t) > len(suf)+3 && strings.HasSuffix(t, suf) {
			return strings.TrimSuffix(t, suf)
		}
	}
	return t
}

func jaccard(a, b []string) float64 {
	if len(a) == 0 && len(b) == 0 {
		return 1
	}
	set := map[string]int{}
	for _, x := range a {
		set[x] |= 1
	}
	for _, x := range b {
		set[x] |= 2
	}
	inter := 0
	for _, v := range set {
		if v == 3 {
			inter++
		}
	}
	return float64(inter) / float64(len(set))
}

// SimilarTactic is the dedupe rule: same category and used_by, and content-word Jaccard ≥ 0.5.
func SimilarTactic(a, b string) bool { return jaccard(NormalizeTactic(a), NormalizeTactic(b)) >= 0.5 }

// MergeTactics upserts one game's findings into the known set. Each known tactic is counted at
// most once per game, so a tactic repeated 5× in one game cannot dominate (overfitting guard).
// The latest counter wins only if the tactic was effective this game (an effective tactic's
// counter is the one we most need to be current).
func MergeTactics(known []KnownTactic, found []Tactic, now time.Time) []KnownTactic {
	out := append([]KnownTactic{}, known...)
	touched, effTouched := map[int]bool{}, map[int]bool{}
	var nextID int64
	for _, k := range out {
		nextID = max(nextID, k.ID)
	}
	for _, f := range found {
		idx := -1
		for i, k := range out {
			if k.Category == f.Category && k.UsedBy == f.UsedBy && SimilarTactic(k.Tactic, f.Tactic) {
				idx = i
				break
			}
		}
		if idx < 0 {
			nextID++
			out = append(out, KnownTactic{ID: nextID, Tactic: f.Tactic, Category: f.Category, UsedBy: f.UsedBy,
				CounterInstruction: f.CounterInstruction})
			idx = len(out) - 1
		}
		if !touched[idx] {
			touched[idx] = true
			out[idx].TimesSeen++
			out[idx].LastSeen = now
		}
		if f.Effective && !effTouched[idx] {
			effTouched[idx] = true
			out[idx].TimesEffective++
			out[idx].CounterInstruction = f.CounterInstruction
		}
	}
	return out
}

// TacticScore ranks known tactics: Wilson lower bound of the effective rate (so 1/1 does not
// beat 8/10), times a 30-day half-life recency factor.
func TacticScore(k KnownTactic, now time.Time) float64 {
	lb := wilsonLower(k.TimesEffective, k.TimesSeen)
	age := now.Sub(k.LastSeen).Hours() / 24
	return (lb + 0.05) * math.Pow(0.5, age/30)
}

func wilsonLower(k, n int) float64 {
	if n == 0 {
		return 0
	}
	const z = 1.96
	p := float64(k) / float64(n)
	nn := float64(n)
	d := 1 + z*z/nn
	c := p + z*z/(2*nn)
	m := z * math.Sqrt(p*(1-p)/nn+z*z/(4*nn*nn))
	return (c - m) / d
}

// SelectOpts bounds what reaches the seat prompt.
type SelectOpts struct {
	TopN        int // KNOWN_TACTICS_TOP_N
	MinSeen     int // tactics seen in fewer games are not injected (one game is an anecdote)
	PerCategory int // cap per category so one theme can't crowd out the rest
	MaxChars    int // hard cap on the rendered block
}

func DefaultSelectOpts() SelectOpts {
	return SelectOpts{TopN: 10, MinSeen: 2, PerCategory: 3, MaxChars: 1200}
}

// SelectTactics picks and renders the known-tactics block for the seat prompt.
func SelectTactics(known []KnownTactic, now time.Time, o SelectOpts) string {
	cands := make([]KnownTactic, 0, len(known))
	for _, k := range known {
		if k.TimesSeen >= o.MinSeen && !unsafeCounterRe.MatchString(k.CounterInstruction) {
			cands = append(cands, k)
		}
	}
	sort.SliceStable(cands, func(i, j int) bool { return TacticScore(cands[i], now) > TacticScore(cands[j], now) })
	var lines []string
	perCat := map[string]int{}
	var picked []KnownTactic
	total := 0
	for _, k := range cands {
		if len(lines) >= o.TopN {
			break
		}
		if o.PerCategory > 0 && perCat[k.Category] >= o.PerCategory {
			continue
		}
		dup := false
		for _, p := range picked {
			if SimilarTactic(p.Tactic, k.Tactic) || SimilarTactic(p.CounterInstruction, k.CounterInstruction) {
				dup = true
				break
			}
		}
		if dup {
			continue
		}
		who := "Humans"
		if k.UsedBy == "ai" {
			who = "AIs"
		}
		line := fmt.Sprintf("- %s %s → %s", who, lowerFirst(TruncateRunes(k.Tactic, maxTacticLen)), TruncateRunes(k.CounterInstruction, maxCounterLen))
		if o.MaxChars > 0 && total+len(line)+1 > o.MaxChars {
			break
		}
		total += len(line) + 1
		lines = append(lines, line)
		picked = append(picked, k)
		perCat[k.Category]++
	}
	return strings.Join(lines, "\n")
}

func lowerFirst(s string) string {
	if s == "" {
		return s
	}
	r := []rune(s)
	if len(r) > 1 && r[1] >= 'a' && r[1] <= 'z' {
		r[0] = []rune(strings.ToLower(string(r[0])))[0]
	}
	return string(r)
}

// ---- director ----

type DirectorOutput struct {
	SeatInstructions map[string]string `json:"seat_instructions"`
	ObservedTactics  []string          `json:"observed_tactics"`
}

const maxInstructionLen = 200

// unsafeInstructionRe: the director reads player text and writes into seat prompts, so its
// output is checked the same way as tactic counters.
var unsafeInstructionRe = unsafeCounterRe

// ParseDirector validates director JSON against the AI seats. Instructions for unknown seats
// (e.g. a human seat) are dropped, over-long ones truncated, unsafe ones dropped.
func ParseDirector(raw string, aiSeats []int) (map[int]string, []string, error) {
	var d DirectorOutput
	if err := json.Unmarshal([]byte(extractJSON(raw)), &d); err != nil {
		return nil, nil, fmt.Errorf("director: %w", err)
	}
	if d.SeatInstructions == nil {
		return nil, nil, errors.New("director: missing seat_instructions")
	}
	valid := map[int]bool{}
	for _, s := range aiSeats {
		valid[s] = true
	}
	out := map[int]string{}
	for k, v := range d.SeatInstructions {
		var seat int
		if _, err := fmt.Sscanf(strings.TrimSpace(k), "%d", &seat); err != nil || !valid[seat] {
			continue
		}
		v = strings.Join(strings.Fields(v), " ")
		if v == "" || unsafeInstructionRe.MatchString(v) {
			continue
		}
		out[seat] = TruncateRunes(v, maxInstructionLen)
	}
	var obs []string
	for _, o := range d.ObservedTactics {
		if o = strings.TrimSpace(o); o != "" && len(obs) < 5 {
			obs = append(obs, TruncateRunes(o, maxTacticLen))
		}
	}
	return out, obs, nil
}

// ParsePersonas validates persona-generator JSON. It returns only valid personas and an error
// if fewer than want are valid or they are not distinct enough (two personas with the same
// case+punctuation+emoji+mood would sound alike: the cross-seat convergence tell).
func ParsePersonas(raw string, want int) ([]Persona, error) {
	var doc struct {
		Personas []Persona `json:"personas"`
	}
	if err := json.Unmarshal([]byte(extractJSON(raw)), &doc); err != nil {
		return nil, fmt.Errorf("personas: %w", err)
	}
	var out []Persona
	var errs []string
	for i, p := range doc.Personas {
		if len(p.Quirks) > 2 {
			p.Quirks = p.Quirks[:2]
		}
		if err := p.Validate(); err != nil {
			errs = append(errs, fmt.Sprintf("#%d %v", i, err))
			continue
		}
		out = append(out, p)
	}
	if len(out) < want {
		return out, fmt.Errorf("personas: %d valid of %d wanted (%s)", len(out), want, strings.Join(errs, "; "))
	}
	out = out[:want]
	for i := range out {
		for j := i + 1; j < len(out); j++ {
			a, b := out[i].Typing, out[j].Typing
			if a.Case == b.Case && a.Punctuation == b.Punctuation && a.Emoji == b.Emoji && out[i].Mood == out[j].Mood {
				return out, fmt.Errorf("personas: #%d and #%d have the same style and mood", i, j)
			}
		}
	}
	return out, nil
}

// ParseVote maps a vote reply to a candidate alias (case-insensitive, substring allowed).
func ParseVote(raw string, candidates []string) (string, bool) {
	r := strings.ToLower(strings.TrimSpace(raw))
	for _, c := range candidates {
		if strings.EqualFold(r, c) {
			return c, true
		}
	}
	var hit string
	n := 0
	for _, c := range candidates {
		if strings.Contains(r, strings.ToLower(c)) {
			hit = c
			n++
		}
	}
	return hit, n == 1
}
