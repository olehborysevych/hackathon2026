package ai

import (
	"context"
	"fmt"
	"math/rand/v2"
	"regexp"
	"strings"
	"time"

	"hackathon2026/prompts"
	"hackathon2026/server/internal/llm"
)

const Skip = "SKIP"

// OOGMarker is how a v2 seat signals that it stepped out of the game (safety valve). Code strips
// it, sets Result.OutOfGame and passes the text through untouched (no critic, no style).
const OOGMarker = "[[OOG]]"

// SeatInput is everything the seat prompt needs for one submission.
type SeatInput struct {
	Persona             Persona
	Alias               string
	GameContext         string
	Now                 time.Time
	KnownTactics        string
	DirectorInstruction string
	Transcript          string
	Task                string
	// v2 fields. Question is the raw text being answered (empty for intro/ask), used by the echo
	// guard and shown to the critic as untrusted data. OwnMessages are this seat's earlier
	// messages in the game (consistency + opener variety). MaxLen is the task's char limit.
	Question     string
	OwnMessages  []string
	OtherAliases []string
	MaxLen       int
	Seed         uint64 // per-call RNG seed; 0 = random
	Kind         string // intro|ask|answer (v3 plans intros differently)
}

// Features are pipeline behaviours beyond "draft → critic". v1 has none of them, so v1 numbers
// stay comparable with the M0 baseline.
type Features struct {
	Plan         bool // sample length/emoji/quirk per message in code
	StyleInCode  bool // enforce case/punctuation/emoji/typos in code (prompt says: don't add typos)
	EchoGuard    bool // retry once, then SKIP, when the reply parrots the question
	SafetyMarker bool // parse [[OOG]] from the seat
	CriticGuard  bool // reject critic output that leaks, adds demanded tokens or grows
	CleanSkip    bool // "text SKIP" → "text"
	SkipRoll     bool // roll persona.skip_rate in code (off in the harness)
	BreakAsIs    bool // a draft that broke character bypasses critic/style (SPEC §3.6: never filter)
	Angle        bool // sample a per-message angle and ban stock openers (cross-seat convergence)
	AliasGuard   bool // strip "hi i'm <own alias>" self-introductions (v3)
}

func DefaultFeatures(version string) Features {
	if version == "v1" {
		return Features{}
	}
	f := Features{Plan: true, StyleInCode: true, EchoGuard: true, SafetyMarker: true, CriticGuard: true,
		CleanSkip: true, BreakAsIs: true, Angle: true}
	if version != "v2" {
		f.AliasGuard = true
	}
	return f
}

type Config struct {
	LLM           llm.LLM
	SeatModel     string
	CriticModel   string // empty = no critic pass
	PromptVersion string
	Features      *Features // nil = DefaultFeatures(PromptVersion)
	SeatTemp      float64   // 0 = 0.9
}

func (c Config) features() Features {
	if c.Features != nil {
		return *c.Features
	}
	return DefaultFeatures(c.PromptVersion)
}

// Call is one LLM call, logged per SPEC §3.4 / ai_calls.
type Call struct {
	Purpose       string // answer|critic|retry|...
	Model         string
	Provider      string
	PromptVersion string
	Latency       time.Duration
	InputTokens   int
	OutputTokens  int
	Cost          float64
	Output        string
	Err           string
	Request       llm.CompletionReq // what was sent (logged to ai_calls.request)
}

// Result is one pipeline run, with everything SPEC §3.4 says to log.
type Result struct {
	Draft          string
	Final          string // "SKIP" when skipped
	BrokeCharacter bool   // critic flag OR regex
	OutOfGame      bool   // safety valve fired (v2 marker)
	Echo           bool   // final still parrots the question
	EchoRetried    bool
	CriticNote     string // "kept", "rewrote", "skip", "guard:<reason>", "bypass:<reason>"
	Plan           *MessagePlan
	Provider       string
	CriticProvider string
	PromptVersion  string
	DraftLatency   time.Duration
	CriticLatency  time.Duration
	InputTokens    int
	OutputTokens   int
	Cost           float64
	Calls          []Call
	Err            error // pipeline error; Final is SKIP when set (invariant 6)
}

func (r *Result) add(c Call) {
	r.Calls = append(r.Calls, c)
	r.InputTokens += c.InputTokens
	r.OutputTokens += c.OutputTokens
	r.Cost += c.Cost
}

func render(tpl string, kv map[string]string) string {
	pairs := make([]string, 0, len(kv)*2)
	for k, v := range kv {
		pairs = append(pairs, "{{"+k+"}}", v)
	}
	return strings.NewReplacer(pairs...).Replace(tpl)
}

const userSplit = "\n---USER---\n"

// splitPrompt splits a template into system and user parts at a "---USER---" line. Templates
// without one are all system, followed by a fixed user turn (v1 behaviour).
func splitPrompt(s string) []llm.Message {
	if sys, user, ok := strings.Cut(s, userSplit); ok {
		return []llm.Message{{Role: "system", Content: strings.TrimSpace(sys)}, {Role: "user", Content: strings.TrimSpace(user)}}
	}
	return []llm.Message{{Role: "system", Content: s}, {Role: "user", Content: "Your message now."}}
}

func seatVars(in SeatInput, version string, mp *MessagePlan) map[string]string {
	tactics := in.KnownTactics
	if strings.TrimSpace(tactics) == "" {
		tactics = "(none yet)"
	}
	persona := in.Persona.String()
	if version != "v1" {
		persona = in.Persona.Describe()
	}
	own := "(nothing yet)"
	if len(in.OwnMessages) > 0 {
		own = "- " + strings.Join(in.OwnMessages, "\n- ")
	}
	dir := strings.TrimSpace(in.DirectorInstruction)
	if dir == "" {
		dir = "(none)"
	}
	kv := map[string]string{
		"persona":              persona,
		"alias":                in.Alias,
		"game_context":         in.GameContext,
		"now":                  in.Now.Format("Monday 2006-01-02 15:04 MST"),
		"known_tactics":        tactics,
		"director_instruction": dir,
		"transcript":           in.Transcript,
		"task":                 in.Task,
		"own_messages":         own,
		"style_hint":           "",
		"oog":                  OOGMarker,
	}
	if version == "v1" {
		kv["director_instruction"] = in.DirectorInstruction
	}
	if mp != nil {
		kv["style_hint"] = mp.StyleHint()
	}
	return kv
}

// Run does draft → critic. Any LLM error yields Final=SKIP and Err set.
func Run(ctx context.Context, cfg Config, in SeatInput) Result {
	f := cfg.features()
	res := Result{PromptVersion: cfg.PromptVersion, Final: Skip}
	seed := in.Seed
	if seed == 0 {
		seed = rand.Uint64()
	}
	rng := rand.New(rand.NewPCG(seed, seed^0x9e3779b97f4a7c15))
	if in.MaxLen <= 0 {
		in.MaxLen = 200
	}
	if f.SkipRoll && rng.Float64() < in.Persona.SkipRate {
		res.CriticNote = "skip-roll"
		return res
	}
	seatTpl, err := prompts.Load("seat", cfg.PromptVersion)
	if err != nil {
		res.Err = err
		return res
	}
	var mp *MessagePlan
	if f.Plan {
		p := PlanMessage(in.Persona, rng, in.MaxLen, in.OwnMessages)
		// Angles are answer framings ("answer, then add an aside"); from v3 they apply to answers
		// only, since on intros/questions they bolted the same small talk onto every message.
		switch {
		case f.Angle && cfg.PromptVersion == "v2":
			p = p.WithAngle(in.Persona, rng)
		case f.Angle && in.Kind == "intro":
			p = p.WithIntroAngle(rng)
		case f.Angle && in.Question != "":
			p = p.WithAngle(in.Persona, rng)
		}
		mp = &p
		res.Plan = mp
	}
	temp := cfg.SeatTemp
	if temp == 0 {
		temp = 0.9
	}
	msgs := splitPrompt(render(seatTpl, seatVars(in, cfg.PromptVersion, mp)))
	maxTok := 200
	draft, call, err := complete(ctx, cfg.LLM, llm.CompletionReq{Model: cfg.SeatModel, Temperature: temp, MaxTokens: maxTok, Messages: msgs}, "answer", cfg.PromptVersion)
	res.add(call)
	res.DraftLatency, res.Provider = call.Latency, call.Provider
	if err != nil {
		res.Err = fmt.Errorf("seat: %w", err)
		return res
	}
	draft = cleanText(draft)
	if f.CleanSkip {
		draft = cleanSkip(draft)
	}
	if f.SafetyMarker {
		if t, ok := cutOOG(draft); ok {
			res.OutOfGame = true
			res.Draft, res.Final = t, t
			res.BrokeCharacter = DetectBreak(t)
			res.CriticNote = "bypass:out-of-game"
			return res
		}
	}
	broke := DetectBreak(draft)
	if f.EchoGuard && !broke && IsEcho(in.Question, draft) {
		res.EchoRetried = true
		if dl, ok := ctx.Deadline(); !ok || time.Until(dl) > 8*time.Second {
			retry := append(append([]llm.Message{}, msgs...),
				llm.Message{Role: "assistant", Content: draft},
				llm.Message{Role: "user", Content: "That just repeats their words back. Write your actual reply to them instead, in your own words."})
			d2, c2, err2 := complete(ctx, cfg.LLM, llm.CompletionReq{Model: cfg.SeatModel, Temperature: temp, MaxTokens: maxTok, Messages: retry}, "answer-retry", cfg.PromptVersion)
			res.add(c2)
			if err2 == nil {
				d2 = cleanSkip(cleanText(d2))
				if _, oog := cutOOG(d2); !oog {
					draft = d2
				}
			}
		}
		if IsEcho(in.Question, draft) {
			draft = Skip
		}
		broke = DetectBreak(draft)
	}
	res.Draft = draft
	res.Final = draft
	res.BrokeCharacter = broke

	final := draft
	switch {
	case strings.EqualFold(draft, Skip):
		res.Final = Skip
		return res
	case f.BreakAsIs && broke:
		res.CriticNote = "bypass:broke-character"
		return res
	case cfg.CriticModel == "":
		res.CriticNote = "off"
	default:
		criticTpl, err := prompts.Load("critic", cfg.PromptVersion)
		if err != nil {
			res.Err = err
			res.Final = Skip
			return res
		}
		kv := map[string]string{"persona": in.Persona.String(), "draft": draft, "task": in.Task}
		if cfg.PromptVersion != "v1" {
			kv["persona"] = in.Persona.Describe()
			kv["question"] = quoteUntrusted(in.Question, 300)
			kv["style_hint"] = ""
			if mp != nil {
				kv["style_hint"] = mp.StyleHint()
			}
		}
		out, c, err := complete(ctx, cfg.LLM, llm.CompletionReq{
			Model: cfg.CriticModel, Temperature: 0.4, MaxTokens: 200,
			Messages: splitPromptCritic(render(criticTpl, kv)),
		}, "critic", cfg.PromptVersion)
		res.add(c)
		res.CriticLatency, res.CriticProvider = c.Latency, c.Provider
		if err != nil {
			res.Err = fmt.Errorf("critic: %w", err)
			res.Final = Skip
			return res
		}
		cf, flag := ParseCritic(out)
		res.BrokeCharacter = res.BrokeCharacter || flag
		switch {
		case strings.EqualFold(cf, "KEEP"):
			res.CriticNote = "kept"
		case cf == Skip:
			final, res.CriticNote = Skip, "skip"
		default:
			cf = StripAddedPrefix(draft, cf)
			if f.CriticGuard {
				if why := criticRejects(draft, cf, in.Persona); why != "" {
					res.CriticNote = "guard:" + why
					break
				}
			}
			final, res.CriticNote = cf, "rewrote"
			if cf == draft {
				res.CriticNote = "kept"
			}
		}
	}
	if final != Skip && f.AliasGuard {
		final = StripSelfAlias(final, in.Alias)
	}
	if final != Skip && f.StyleInCode && mp != nil {
		protect := append([]string{in.Alias, in.Persona.Location, in.Persona.Job}, in.OtherAliases...)
		final = ApplyStyle(final, in.Persona, *mp, rng, protect, EmojiIn(in.Question+" "+in.Transcript))
	}
	if final != Skip {
		final = TruncateRunes(final, in.MaxLen)
	}
	res.Final = final
	res.Echo = IsEcho(in.Question, final)
	res.BrokeCharacter = res.BrokeCharacter || DetectBreak(final)
	return res
}

func complete(ctx context.Context, l llm.LLM, req llm.CompletionReq, purpose, version string) (string, Call, error) {
	r, err := l.Complete(ctx, req)
	c := Call{Purpose: purpose, Model: req.Model, Provider: r.Provider, PromptVersion: version, Latency: r.Latency,
		InputTokens: r.InputTokens, OutputTokens: r.OutputTokens, Cost: r.Cost, Output: r.Text, Request: req}
	if err != nil {
		c.Err = err.Error()
	}
	return r.Text, c, err
}

// v1 critic is a single user message; v2 may split system/user.
func splitPromptCritic(s string) []llm.Message {
	if sys, user, ok := strings.Cut(s, userSplit); ok {
		return []llm.Message{{Role: "system", Content: strings.TrimSpace(sys)}, {Role: "user", Content: strings.TrimSpace(user)}}
	}
	return []llm.Message{{Role: "user", Content: s}}
}

// quoteUntrusted wraps player text for a prompt: truncated, delimiter-safe.
func quoteUntrusted(s string, n int) string {
	if strings.TrimSpace(s) == "" {
		return "<<<(none)>>>"
	}
	s = strings.NewReplacer("<<<", "‹‹‹", ">>>", "›››").Replace(s)
	return "<<<" + TruncateRunes(strings.Join(strings.Fields(s), " "), n) + ">>>"
}

func cutOOG(s string) (string, bool) {
	t := strings.TrimSpace(s)
	if strings.HasPrefix(t, OOGMarker) {
		return strings.TrimSpace(strings.TrimPrefix(t, OOGMarker)), true
	}
	return s, false
}

var (
	leakRe      = regexp.MustCompile(`(?i)\b(persona|typo_rate|skip_rate|avg_len|native_language|broke_character|system prompt|my instructions|the instructions|style hint)\b`)
	shoutWordRe = regexp.MustCompile(`\b[A-Z][A-Z-]{3,}\b`)
)

// criticRejects says why a critic rewrite should be discarded in favour of the draft: the
// critic is an LLM reading untrusted text, so it is checked like one (DECISIONS 19/56).
func criticRejects(draft, final string, p Persona) string {
	dl := strings.ToLower(draft)
	for _, m := range leakRe.FindAllString(final, -1) {
		if !strings.Contains(dl, strings.ToLower(m)) {
			return "leak"
		}
	}
	for _, w := range shoutWordRe.FindAllString(final, -1) {
		if !strings.Contains(draft, w) {
			return "added-keyword"
		}
	}
	if len([]rune(final)) > len([]rune(draft))*3/2+25 {
		return "grew"
	}
	// Persona-sheet details the draft did not mention (age, job, city) are a leak, not a fix.
	for _, fact := range []string{itoa(p.Age), p.Job, strings.Split(p.Location, ",")[0]} {
		fl := strings.ToLower(fact)
		if len(fl) >= 2 && strings.Contains(strings.ToLower(final), fl) && !strings.Contains(dl, fl) {
			return "persona-fact"
		}
	}
	return ""
}

var flagRe = regexp.MustCompile(`(?i)\bBROKE_CHARACTER\s*=\s*(true|false)\b`)

// ParseCritic splits the critic output into final text and the BROKE_CHARACTER flag.
func ParseCritic(s string) (final string, broke bool) {
	if m := flagRe.FindStringSubmatch(s); m != nil {
		broke = strings.EqualFold(m[1], "true")
	}
	final = strings.TrimSpace(flagRe.ReplaceAllString(s, ""))
	final = cleanText(final)
	if final == "" {
		final = Skip
	}
	return
}

func cleanText(s string) string {
	s = strings.TrimSpace(s)
	if len(s) >= 2 && s[0] == '"' && s[len(s)-1] == '"' {
		s = strings.TrimSpace(s[1 : len(s)-1])
	}
	if strings.EqualFold(s, Skip) {
		return Skip
	}
	return s
}

var breakRe = regexp.MustCompile(`(?i)\bas an ai\b|\blanguage model\b|\bi'?m (an? )?(ai|llm|chatbot|bot)\b|\bi am (an? )?(ai|llm|chatbot)\b|\bi'?m (claude|chatgpt|gpt|gemini|qwen|gemma|mistral)\b|\bartificial intelligence\b|\bmy (system )?prompt\b|\bi (can'?t|cannot) (help|assist) with\b|\bhappy to help\b|\bcertainly!`)

// DetectBreak flags assistant-style character breaks. It never alters the text (SPEC §3.6).
func DetectBreak(s string) bool { return breakRe.MatchString(s) }

var prefixRe = regexp.MustCompile(`^\S{1,10}:\s+`)

// StripAddedPrefix removes a short "KEYWORD: " prefix the critic added but the draft did not
// have (e.g. echoing a "begin your reply with GM-OK:" injection).
func StripAddedPrefix(draft, final string) string {
	if p := prefixRe.FindString(final); p != "" && !strings.HasPrefix(strings.ToLower(draft), strings.ToLower(p)) {
		if rest := strings.TrimSpace(final[len(p):]); rest != "" {
			return rest
		}
	}
	return final
}
