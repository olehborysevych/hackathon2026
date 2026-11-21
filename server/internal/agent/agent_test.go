package agent

import (
	"context"
	"errors"
	"math/rand/v2"
	"strings"
	"testing"
	"time"

	"hackathon2026/server/internal/game"
	"hackathon2026/server/internal/ws"
)

func answerReq() Request {
	return Request{Seat: 2, Phase: game.Answer, Round: 1, Questions: []ws.Question{
		{ID: "q1", Seat: 0, Text: "a?"}, {ID: "q2", Seat: 1, Text: "b?"},
	}}
}

func TestResolveFailureIsSkip(t *testing.T) {
	for _, err := range []error{errors.New("boom"), context.DeadlineExceeded} {
		subs, _ := Resolve(answerReq(), Reply{Items: []Item{{QuestionID: "q1", Text: "x"}}}, err)
		if len(subs) != 2 {
			t.Fatalf("want 2 slots, got %d", len(subs))
		}
		for _, s := range subs {
			if !s.Skip || s.Text != "" {
				t.Fatalf("failure not a skip: %+v", s)
			}
		}
	}
}

func TestResolveCleansReply(t *testing.T) {
	long := strings.Repeat("ü", game.MaxAnswer+40)
	rep := Reply{Items: []Item{
		{QuestionID: "q1", Text: long},
		{QuestionID: "q1", Text: "duplicate"},
		{QuestionID: "nope", Text: "unknown question"},
	}}
	subs, _ := Resolve(answerReq(), rep, nil)
	if len(subs) != 2 {
		t.Fatalf("want 2, got %d", len(subs))
	}
	if subs[0].QuestionID != "q1" || subs[0].Skip || len([]rune(subs[0].Text)) != game.MaxAnswer {
		t.Fatalf("q1 not truncated: %+v", subs[0])
	}
	if subs[1].QuestionID != "q2" || !subs[1].Skip {
		t.Fatalf("missing q2 not skipped: %+v", subs[1])
	}
	subs, _ = Resolve(Request{Seat: 1, Phase: game.Intro}, Reply{Items: []Item{{Text: "   "}}}, nil)
	if len(subs) != 1 || !subs[0].Skip {
		t.Fatalf("blank intro not a skip: %+v", subs)
	}
}

func TestResolveVote(t *testing.T) {
	req := Request{Seat: 2, Phase: game.Vote, Candidates: []int{0, 1, 3, 4}}
	v := 3
	if _, got := Resolve(req, Reply{Vote: &v}, nil); got == nil || *got != 3 {
		t.Fatal("valid vote dropped")
	}
	self := 2
	if _, got := Resolve(req, Reply{Vote: &self}, nil); got != nil {
		t.Fatal("self vote kept")
	}
	if _, got := Resolve(req, Reply{Vote: &v}, errors.New("x")); got != nil {
		t.Fatal("vote kept on error")
	}
}

func TestStubHonoursDeadline(t *testing.T) {
	s := NewStub(1)
	s.MinDelay, s.MaxDelay = time.Hour, time.Hour
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Millisecond)
	defer cancel()
	start := time.Now()
	if _, err := s.Act(ctx, Request{Phase: game.Intro}); err == nil {
		t.Fatal("no error after deadline")
	}
	if time.Since(start) > time.Second {
		t.Fatal("stub ignored the deadline")
	}
}

// Every agent outcome, fed back through the game's normal submit path, is accepted and fills
// the seat's slots, so a failing pipeline can never hold a phase open (invariant 6).
func TestAgentOutcomesFillSlots(t *testing.T) {
	specs := []game.SeatSpec{
		{Kind: game.KindHuman, UserID: "u1"}, {Kind: game.KindHuman, UserID: "u2"},
		{Kind: game.KindAI}, {Kind: game.KindAI}, {Kind: game.KindAI},
	}
	stub := NewStub(7)
	stub.MinDelay, stub.MaxDelay = 0, 0
	outcomes := []func(Request) (Reply, error){
		func(r Request) (Reply, error) { return stub.Act(context.Background(), r) },
		func(Request) (Reply, error) { return Reply{}, errors.New("llm down") },
		func(Request) (Reply, error) { return Reply{}, context.DeadlineExceeded },
		func(Request) (Reply, error) { return Reply{Items: []Item{{QuestionID: "bogus", Text: "?"}}}, nil },
	}
	for i, act := range outcomes {
		g := game.New("g", game.DefaultConfig(), specs, rand.New(rand.NewPCG(uint64(i), 1)))
		now := time.Now()
		pending := collectRuns(g.Start(now))
		humans := map[int]bool{}
		for _, s := range g.Seats() {
			humans[s.Index] = s.Kind == game.KindHuman
		}
		for steps := 0; g.Phase() != game.Result; steps++ {
			if steps > 40 {
				t.Fatalf("outcome %d: game stuck in %s", i, g.Phase())
			}
			var next []game.RunAgent
			for _, ra := range pending {
				req := Request{Seat: ra.Seat, Phase: ra.Phase, Round: ra.Round, Questions: ra.Questions, Candidates: ra.Candidates}
				rep, err := act(req)
				subs, vote := Resolve(req, rep, err)
				for _, s := range subs {
					eff, err := g.Submit(now, s)
					if err != nil {
						t.Fatalf("outcome %d: resolved submission rejected: %v", i, err)
					}
					next = append(next, collectRuns(eff)...)
				}
				if vote != nil {
					eff, err := g.CastVote(now, ra.Seat, *vote)
					if err != nil {
						t.Fatal(err)
					}
					next = append(next, collectRuns(eff)...)
				}
			}
			// Humans do nothing; the AI seats alone must have filled their slots, so only
			// the deadline is left (REVEAL and phases waiting for humans).
			if len(next) == 0 {
				now = g.Deadline()
				next = collectRuns(g.Tick(now))
			}
			pending = next
		}
	}
}

func collectRuns(eff []game.Effect) []game.RunAgent {
	var out []game.RunAgent
	for _, e := range eff {
		if ra, ok := e.(game.RunAgent); ok {
			out = append(out, ra)
		}
	}
	return out
}

func TestStubSeatsSayDifferentThings(t *testing.T) {
	s := NewStub(3)
	s.MinDelay, s.MaxDelay, s.SkipRate = 0, 0, 0
	for _, ph := range []game.Phase{game.Intro, game.Ask} {
		seen := map[string]bool{}
		for seat := 0; seat < 5; seat++ {
			rep, _ := s.Act(context.Background(), Request{GameID: "g1", Seat: seat, Phase: ph, Round: 1})
			if seen[rep.Items[0].Text] {
				t.Fatalf("%s: two seats said %q", ph, rep.Items[0].Text)
			}
			seen[rep.Items[0].Text] = true
		}
	}
}
