package agent

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"hash/fnv"
	"math/rand/v2"
	"strings"
	"sync"
	"time"

	"hackathon2026/server/internal/ai"
	"hackathon2026/server/internal/game"
	"hackathon2026/server/internal/llm"
	"hackathon2026/server/internal/ws"
)

// LLM plays ai seats with the real pipeline (internal/ai: seat prompt → optional critic → code
// style pass). bot_human seats go to Fallback (the stub), so one person can still test alone.
//
// Each Act makes one pipeline run per slot (ANSWER: one per question, in parallel) and returns
// every LLM call in Reply.Calls. Failures and timeouts come back as skips through Resolve, never
// as a stuck phase (invariant 6).
type LLM struct {
	Client        llm.LLM
	CriticModel   string // "" = no critic (recommended for v2, DECISIONS 66)
	PromptVersion string
	GameContext   string           // GAME_CONTEXT, e.g. "Hackathon at the Innovation Hub, Berlin"
	KnownTactics  func() string    // optional; rendered known-tactics block (ai.SelectTactics)
	Fallback      Agent            // plays bot_human seats
	Now           func() time.Time // clock for the prompt's date/time; nil = time.Now
	Location      *time.Location   // venue time zone for the prompt; nil = server local
	Features      *ai.Features     // nil = ai.DefaultFeatures(PromptVersion)
	// IntroExtraSkip is added to the persona's skip_rate for the optional intro (humans often
	// skip it). The server uses 0.15.
	IntroExtraSkip float64
}

var errNoPersona = errors.New("agent: ai seat without a persona")

func (a *LLM) Act(ctx context.Context, req Request) (Reply, error) {
	if req.Kind != game.KindAI {
		if a.Fallback == nil {
			return Reply{}, errors.New("agent: no fallback for non-ai seat")
		}
		return a.Fallback.Act(ctx, req)
	}
	var p ai.Persona
	if len(req.Persona) == 0 {
		return Reply{}, errNoPersona
	}
	if err := json.Unmarshal(req.Persona, &p); err != nil {
		return Reply{}, fmt.Errorf("agent: persona: %w", err)
	}
	if req.Phase == game.Vote {
		return a.vote(ctx, req)
	}

	tr := buildTranscript(req)
	base := ai.SeatInput{
		Persona: p, Alias: req.Alias, GameContext: a.GameContext, Now: a.now(),
		Transcript: tr.text, OwnMessages: tr.own, OtherAliases: tr.others,
	}
	if a.KnownTactics != nil {
		base.KnownTactics = a.KnownTactics()
	}
	cfg := ai.Config{LLM: a.Client, SeatModel: req.ModelID, CriticModel: a.CriticModel, PromptVersion: a.PromptVersion, Features: a.Features}

	type slot struct {
		qid string
		in  ai.SeatInput
	}
	var slots []slot
	switch req.Phase {
	case game.Intro:
		in := base
		in.Transcript = "(intros are revealed together; nothing yet)"
		in.Task = ai.IntroTask(a.PromptVersion, game.MaxIntro)
		in.Kind = "intro"
		in.MaxLen = game.MaxIntro
		slots = append(slots, slot{"", in})
	case game.Ask:
		in := base
		in.Task = ai.AskTask(a.PromptVersion, game.MaxAsk)
		in.Kind = "ask"
		in.MaxLen = game.MaxAsk
		slots = append(slots, slot{"", in})
	case game.Answer:
		for _, q := range req.Questions {
			asker := tr.alias[q.Seat]
			in := base
			in.Question = q.Text
			in.Transcript = tr.text + fmt.Sprintf("\n[Round %d] Question from %s: %s", req.Round, asker, q.Text)
			in.Task = ai.AnswerTask(asker, q.Text, game.MaxAnswer)
			in.Kind = "answer"
			in.MaxLen = game.MaxAnswer
			slots = append(slots, slot{q.ID, in})
		}
	default:
		return Reply{}, nil
	}

	results := make([]ai.Result, len(slots))
	var wg sync.WaitGroup
	for i := range slots {
		// Seed the slot itself, not a loop copy: the skip_rate roll below reads slots[i].in.Seed,
		// and an unseeded roll is the same number for every seat and game.
		slots[i].in.Seed = seed(req.GameID, req.Seat, string(req.Phase), req.Round, slots[i].qid)
		s := slots[i]
		wg.Add(1)
		go func() {
			defer wg.Done()
			results[i] = ai.Run(ctx, cfg, s.in)
		}()
	}
	wg.Wait()

	var rep Reply
	purpose := strings.ToLower(string(req.Phase))
	for i, r := range results {
		it := Item{QuestionID: slots[i].qid, Text: r.Final, BrokeCharacter: r.BrokeCharacter, OutOfGame: r.OutOfGame}
		// skip_rate is rolled after the call, and never silences a safety-valve reply. Intros are
		// optional and humans often skip them, so they get extra skip probability.
		rng := rand.New(rand.NewPCG(slots[i].in.Seed, 7))
		skip := p.SkipRate
		if req.Phase == game.Intro {
			skip += a.IntroExtraSkip
		}
		if r.Final == ai.Skip || r.Final == "" || (!r.OutOfGame && rng.Float64() < skip) {
			it.Text, it.Skip = "", true
		}
		rep.Items = append(rep.Items, it)
		rep.Calls = append(rep.Calls, callRecords(r, purpose, a.PromptVersion)...)
	}
	return rep, nil
}

func (a *LLM) vote(ctx context.Context, req Request) (Reply, error) {
	tr := buildTranscript(req)
	var names []string
	bySeat := map[string]int{}
	for _, c := range req.Candidates {
		names = append(names, tr.alias[c])
		bySeat[tr.alias[c]] = c
	}
	if len(names) == 0 {
		return Reply{}, nil
	}
	pick, call, err := ai.Vote(ctx, a.Client, req.ModelID, req.Alias, names, tr.text)
	rep := Reply{Calls: []CallRecord{toRecord(call, "vote", "v1", "", pick)}}
	if err != nil && call.Output == "" {
		return rep, err // no answer at all: no vote
	}
	if s, ok := bySeat[pick]; ok {
		rep.Vote = &s
	}
	return rep, nil
}

func (a *LLM) now() time.Time {
	t := time.Now()
	if a.Now != nil {
		t = a.Now()
	}
	if a.Location != nil {
		t = t.In(a.Location)
	}
	return t
}

func seed(parts ...any) uint64 {
	h := fnv.New64a()
	fmt.Fprint(h, parts...)
	return h.Sum64() | 1
}

// callRecords turns a pipeline result into ai_calls rows: the seat call(s) carry the draft and
// the final text, the critic call carries its own output.
func callRecords(r ai.Result, purpose, version string) []CallRecord {
	var out []CallRecord
	for _, c := range r.Calls {
		p := purpose
		draft, final := "", c.Output
		switch c.Purpose {
		case "critic":
			p, draft, final = "critic", r.Draft, c.Output
		case "answer":
			draft, final = c.Output, r.Final
		}
		out = append(out, toRecord(c, p, version, draft, final))
	}
	if len(out) == 0 && r.Err != nil { // e.g. prompt failed to load
		out = append(out, CallRecord{Purpose: purpose, PromptVersion: version, Error: r.Err.Error(), Final: ai.Skip})
	}
	return out
}

func toRecord(c ai.Call, purpose, version, draft, final string) CallRecord {
	req, _ := json.Marshal(struct {
		Model       string        `json:"model"`
		Temperature float64       `json:"temperature"`
		MaxTokens   int           `json:"max_tokens,omitempty"`
		Messages    []llm.Message `json:"messages"`
	}{c.Request.Model, c.Request.Temperature, c.Request.MaxTokens, c.Request.Messages})
	if c.PromptVersion != "" {
		version = c.PromptVersion
	}
	return CallRecord{
		Purpose: purpose, Provider: c.Provider, ModelID: c.Model, PromptVersion: version, Request: req,
		Draft: draft, Final: final, LatencyMs: int(c.Latency.Milliseconds()),
		InputTokens: c.InputTokens, OutputTokens: c.OutputTokens, CostUSD: c.Cost, Error: c.Err,
		CreatedAt: time.Now(),
	}
}

type transcript struct {
	text   string
	own    []string
	others []string
	alias  map[int]string
}

// buildTranscript renders the public reveals for one seat: aliases only (invariant 3), its own
// lines marked "(you)", skips shown as "—" like the table shows them.
func buildTranscript(req Request) transcript {
	tr := transcript{alias: map[int]string{}}
	for _, s := range req.Seats {
		tr.alias[s.Seat] = s.Alias
		if s.Seat != req.Seat {
			tr.others = append(tr.others, s.Alias)
		}
	}
	name := func(seat int) string {
		n := tr.alias[seat]
		if n == "" {
			n = fmt.Sprintf("Seat %d", seat)
		}
		if seat == req.Seat {
			n += " (you)"
		}
		return n
	}
	questions := map[string]ws.RevealItem{}
	var lines []string
	for _, ph := range req.Transcript {
		for _, it := range ph.Items {
			text := "—"
			if it.Text != nil {
				text = *it.Text
				if it.Seat == req.Seat {
					tr.own = append(tr.own, text)
				}
			}
			switch it.Kind {
			case string(game.MsgIntro):
				lines = append(lines, fmt.Sprintf("[Intro] %s: %s", name(it.Seat), text))
			case string(game.MsgQuestion):
				questions[it.ID] = it
				if it.Text != nil {
					lines = append(lines, fmt.Sprintf("[Round %d] Question from %s: %s", ph.Round, name(it.Seat), text))
				}
			case string(game.MsgAnswer):
				to := "a question"
				if q, ok := questions[it.QuestionID]; ok {
					to = tr.alias[q.Seat] + "'s question"
				}
				lines = append(lines, fmt.Sprintf("[Round %d] Answer from %s to %s: %s", ph.Round, name(it.Seat), to, text))
			}
		}
	}
	tr.text = strings.Join(lines, "\n")
	if tr.text == "" {
		tr.text = "(nothing yet)"
	}
	return tr
}
