package agent

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"sync"
	"testing"

	"hackathon2026/server/internal/ai"
	"hackathon2026/server/internal/game"
	"hackathon2026/server/internal/llm"
	"hackathon2026/server/internal/ws"
)

// scriptLLM answers by looking at the last user message, so parallel calls are deterministic.
type scriptLLM struct {
	mu    sync.Mutex
	reply func(req llm.CompletionReq) (string, error)
	reqs  []llm.CompletionReq
}

func (s *scriptLLM) Complete(_ context.Context, req llm.CompletionReq) (llm.CompletionResp, error) {
	s.mu.Lock()
	s.reqs = append(s.reqs, req)
	s.mu.Unlock()
	text, err := s.reply(req)
	return llm.CompletionResp{Text: text, Provider: "fake-provider", InputTokens: 100, OutputTokens: 10, Cost: 0.0002}, err
}

func lastUser(req llm.CompletionReq) string { return req.Messages[len(req.Messages)-1].Content }

func persona(skip float64) json.RawMessage {
	p := ai.PersonaPool[0]
	p.SkipRate = skip
	b, _ := json.Marshal(p)
	return b
}

func str(s string) *string { return &s }

func tableReq(phase game.Phase) Request {
	return Request{
		GameID: "g1", Seat: 2, Alias: "Amber Owl", Kind: game.KindAI, ModelID: "seat-model", Persona: persona(0),
		Phase: phase, Round: 1,
		Seats: []ws.PublicSeat{{Seat: 0, Alias: "Blue Fox"}, {Seat: 1, Alias: "Green Newt"}, {Seat: 2, Alias: "Amber Owl"}, {Seat: 3, Alias: "Red Crow"}},
		Transcript: []ws.PhaseReveal{
			{Phase: "INTRO", Round: 0, Items: []ws.RevealItem{
				{ID: "i0", Seat: 0, Kind: "intro", Text: str("hi all")},
				{ID: "i1", Seat: 1, Kind: "intro"}, // skipped
				{ID: "i2", Seat: 2, Kind: "intro", Text: str("hey, here with two guys from work")},
			}},
		},
	}
}

func newLLM(s *scriptLLM) *LLM {
	return &LLM{Client: s, PromptVersion: "v2", GameContext: "Hackathon, Berlin", Fallback: NewStub(1)}
}

func TestLLMAnswersEachQuestion(t *testing.T) {
	s := &scriptLLM{reply: func(req llm.CompletionReq) (string, error) {
		if strings.Contains(lastUser(req), "breakfast") {
			return "toast and coffee", nil
		}
		return "dogs obviously", nil
	}}
	req := tableReq(game.Answer)
	req.Questions = []ws.Question{{ID: "q1", Seat: 0, Text: "what did you have for breakfast"}, {ID: "q2", Seat: 3, Text: "cats or dogs"}}
	rep, err := newLLM(s).Act(context.Background(), req)
	if err != nil {
		t.Fatal(err)
	}
	subs, _ := Resolve(req, rep, nil)
	got := map[string]string{}
	for _, sub := range subs {
		got[sub.QuestionID] = sub.Text
	}
	if !strings.Contains(got["q1"], "toast") || !strings.Contains(got["q2"], "dogs") {
		t.Fatalf("answers mapped wrong: %v", got)
	}
	if len(rep.Calls) != 2 || rep.Calls[0].Purpose != "answer" || rep.Calls[0].Provider != "fake-provider" ||
		rep.Calls[0].InputTokens != 100 || rep.Calls[0].CostUSD == 0 || len(rep.Calls[0].Request) == 0 || rep.Calls[0].Draft == "" {
		t.Fatalf("calls not logged fully: %+v", rep.Calls)
	}
	// The prompt shows aliases, marks own lines, shows skips as "—", and names the asker.
	prompt := lastUser(s.reqs[0])
	for _, want := range []string{"[Intro] Blue Fox: hi all", "[Intro] Green Newt: —", "Amber Owl (you): hey, here with two guys", "Question from"} {
		if !strings.Contains(prompt, want) {
			t.Errorf("prompt missing %q:\n%s", want, prompt)
		}
	}
	if s.reqs[0].Model != "seat-model" {
		t.Errorf("seat model not used: %s", s.reqs[0].Model)
	}
}

func TestLLMIntroAndAsk(t *testing.T) {
	s := &scriptLLM{reply: func(req llm.CompletionReq) (string, error) { return "hey, here for the pizza", nil }}
	for _, ph := range []game.Phase{game.Intro, game.Ask} {
		req := tableReq(ph)
		rep, err := newLLM(s).Act(context.Background(), req)
		subs, _ := Resolve(req, rep, err)
		if err != nil || len(subs) != 1 || subs[0].Skip || rep.Calls[0].Purpose != strings.ToLower(string(ph)) {
			t.Fatalf("%s: %v %+v %+v", ph, err, subs, rep.Calls)
		}
	}
}

func TestLLMFallbackForBotHuman(t *testing.T) {
	s := &scriptLLM{reply: func(llm.CompletionReq) (string, error) { t.Fatal("LLM called for bot_human"); return "", nil }}
	req := tableReq(game.Intro)
	req.Kind, req.Persona = game.KindBotHuman, nil
	if _, err := newLLM(s).Act(context.Background(), req); err != nil {
		t.Fatal(err)
	}
}

func TestLLMFailuresAreSkips(t *testing.T) {
	// Invariant 6: LLM error → skip, with the failed call still logged.
	s := &scriptLLM{reply: func(llm.CompletionReq) (string, error) { return "", errors.New("upstream 503") }}
	req := tableReq(game.Answer)
	req.Questions = []ws.Question{{ID: "q1", Seat: 0, Text: "hi?"}}
	rep, err := newLLM(s).Act(context.Background(), req)
	subs, _ := Resolve(req, rep, err)
	if len(subs) != 1 || !subs[0].Skip || len(rep.Calls) != 1 || rep.Calls[0].Error == "" {
		t.Fatalf("error must be a logged skip: %+v %+v", subs, rep.Calls)
	}
	// No persona → error → skip.
	req.Persona = nil
	_, err = newLLM(s).Act(context.Background(), req)
	if subs, _ := Resolve(req, Reply{}, err); err == nil || !subs[0].Skip {
		t.Fatal("missing persona must be a skip")
	}
}

func TestLLMSkipRollNeverSilencesSafetyValve(t *testing.T) {
	s := &scriptLLM{reply: func(req llm.CompletionReq) (string, error) {
		if strings.Contains(lastUser(req), "collapsed") {
			return ai.OOGMarker + " Call 112 now and get the organisers. I'm one of the AI players.", nil
		}
		return "toast", nil
	}}
	req := tableReq(game.Answer)
	req.Persona = persona(1) // always skip
	req.Questions = []ws.Question{{ID: "q1", Seat: 0, Text: "someone collapsed and isn't responding, what do i do"}, {ID: "q2", Seat: 3, Text: "breakfast?"}}
	rep, _ := newLLM(s).Act(context.Background(), req)
	subs, _ := Resolve(req, rep, nil)
	for _, sub := range subs {
		switch sub.QuestionID {
		case "q1":
			if sub.Skip || !sub.OutOfGame || !strings.Contains(sub.Text, "112") {
				t.Errorf("safety valve reply silenced or unflagged: %+v", sub)
			}
		case "q2":
			if !sub.Skip {
				t.Errorf("skip_rate 1 should skip a normal reply: %+v", sub)
			}
		}
	}
}

func TestLLMBreakFlagReachesSubmission(t *testing.T) {
	s := &scriptLLM{reply: func(llm.CompletionReq) (string, error) { return "As an AI language model I can't say", nil }}
	req := tableReq(game.Answer)
	req.Questions = []ws.Question{{ID: "q1", Seat: 0, Text: "are you a bot"}}
	rep, _ := newLLM(s).Act(context.Background(), req)
	subs, _ := Resolve(req, rep, nil)
	if !subs[0].BrokeCharacter || subs[0].Text != "As an AI language model I can't say" {
		t.Fatalf("break must be flagged and shown as-is: %+v", subs[0])
	}
}

func TestLLMVote(t *testing.T) {
	s := &scriptLLM{reply: func(llm.CompletionReq) (string, error) { return "Red Crow", nil }}
	req := tableReq(game.Vote)
	req.Candidates = []int{0, 1, 3}
	rep, err := newLLM(s).Act(context.Background(), req)
	if err != nil || rep.Vote == nil || *rep.Vote != 3 || rep.Calls[0].Purpose != "vote" {
		t.Fatalf("vote: %v %+v", err, rep)
	}
	s.reply = func(llm.CompletionReq) (string, error) { return "no idea tbh", nil }
	rep, _ = newLLM(s).Act(context.Background(), req)
	if _, v := Resolve(req, rep, nil); v != nil && (*v != 0 && *v != 1 && *v != 3) {
		t.Fatalf("vote outside candidates: %v", *v)
	}
}
