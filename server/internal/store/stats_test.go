package store

import (
	"context"
	"fmt"
	"math/rand/v2"
	"testing"
	"time"

	"hackathon2026/server/internal/game"
)

// voteGame runs a game through the state machine with every phase left to its deadline, then
// lets vote pick each seat's vote from the shuffled seats. Scoring is the real game.score.
func voteGame(t *testing.T, s *Store, n int, difficulty, prompt string, specs []game.SeatSpec, vote func([]game.Seat) map[int]int) {
	t.Helper()
	ctx := context.Background()
	id := fmt.Sprintf("00000000-0000-4000-8000-%012d", n)
	g := game.New(id, game.DefaultConfig(), specs, rand.New(rand.NewPCG(uint64(n), 7)))
	now := time.Date(2026, 11, 1, 12, 0, 0, 0, time.UTC).Add(time.Duration(n) * time.Hour)
	write := func(eff []game.Effect) {
		for _, e := range eff {
			p, ok := e.(game.Persist)
			if !ok {
				continue
			}
			var err error
			switch r := p.Record.(type) {
			case game.GameRecord:
				r.Difficulty, r.PromptVersion = difficulty, prompt
				err = s.CreateGame(ctx, r)
			case game.MessageRecord:
				err = s.InsertMessage(ctx, r)
			case game.VoteRecord:
				err = s.InsertVote(ctx, r)
			case game.FinishRecord:
				err = s.FinishGame(ctx, r)
			}
			if err != nil {
				t.Fatalf("%T: %v", p.Record, err)
			}
		}
	}
	write(g.Start(now))
	for g.Phase() != game.Result {
		if g.Phase() == game.Vote {
			for voter, target := range vote(g.Seats()) {
				eff, err := g.CastVote(now, voter, target)
				if err != nil {
					t.Fatal(err)
				}
				write(eff)
			}
		}
		if g.Phase() != game.Result {
			now = g.Deadline()
			write(g.Tick(now))
		}
	}
}

// seatsOf returns the seat indexes of one kind (and, for AI, one model) in seat order.
func seatsOf(seats []game.Seat, kind game.SeatKind, model string) []int {
	var out []int
	for _, st := range seats {
		if st.Kind == kind && (model == "" || st.ModelID == model) {
			out = append(out, st.Index)
		}
	}
	return out
}

func TestStatsReadRealGamesOnly(t *testing.T) {
	s := testStore(t)
	ctx := context.Background()
	a, _ := s.CreateUser(ctx, "alice", "x", time.Now())
	b, _ := s.CreateUser(ctx, "bob", "x", time.Now())
	ai := func(m string) game.SeatSpec { return game.SeatSpec{Kind: game.KindAI, Provider: "p", ModelID: m} }
	human := func(u User) game.SeatSpec { return game.SeatSpec{Kind: game.KindHuman, UserID: u.ID} }

	// Game 1 (independent, v1): mutual match. 2 human votes, 0 stolen.
	voteGame(t, s, 1, "independent", "v1", []game.SeatSpec{human(a), human(b), ai("m1"), ai("m1"), ai("m2")},
		func(st []game.Seat) map[int]int {
			h := seatsOf(st, game.KindHuman, "")
			return map[int]int{h[0]: h[1], h[1]: h[0]}
		})
	// Game 2 (directed, v2): both humans vote for an m2 seat. 2 votes, 2 stolen by m2.
	voteGame(t, s, 2, "directed", "v2", []game.SeatSpec{human(a), human(b), ai("m1"), ai("m2"), ai("m2")},
		func(st []game.Seat) map[int]int {
			h, m2 := seatsOf(st, game.KindHuman, ""), seatsOf(st, game.KindAI, "m2")
			out := map[int]int{h[0]: m2[0], h[1]: m2[1]}
			for _, x := range seatsOf(st, game.KindAI, "") { // AI votes are logged, never counted
				out[x] = h[0]
			}
			return out
		})
	// Game 3 with a bot_human partner: alice votes for an AI. Must not count anywhere.
	voteGame(t, s, 3, "independent", "v1", []game.SeatSpec{human(a), {Kind: game.KindBotHuman}, ai("m1"), ai("m1"), ai("m3")},
		func(st []game.Seat) map[int]int {
			return map[int]int{seatsOf(st, game.KindHuman, "")[0]: seatsOf(st, game.KindAI, "m3")[0]}
		})
	var n int
	_ = s.pool.QueryRow(ctx, `select count(*) from games where status='finished'`).Scan(&n)
	if n != 3 {
		t.Fatalf("finished games: %d", n)
	}

	// Leaderboard totals: two games each, the bot_human game is not one of them.
	totals, err := s.PlayerTotals(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if len(totals) != 2 {
		t.Fatalf("players: %+v", totals)
	}
	for _, p := range totals {
		if p.Games != 2 || p.Points != 3 || p.Found != 1 || p.WasFound != 1 || p.Mutual != 1 {
			t.Errorf("%s: %+v", p.Username, p)
		}
	}
	recent, err := s.RecentGames(ctx, a.ID, 5)
	if err != nil {
		t.Fatal(err)
	}
	if len(recent) != 2 || recent[0].GameID != "00000000-0000-4000-8000-000000000002" || recent[1].Points != 3 {
		t.Fatalf("recent: %+v", recent)
	}

	// Fool rates: 4 human votes, 2 stolen; m3 (bot_human game only) never shows up.
	fr, err := s.FoolRates(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if fr.Votes != 4 || fr.Stolen != 2 || fr.Games != 2 || fr.AISeatVotes != 2*3+2*3 {
		t.Fatalf("overall: %+v", fr)
	}
	want := map[string]FoolGroup{
		// game 1: 2 votes × 2 m1 seats; game 2: 2 votes × 1 m1 seat
		"m1": {Key: "m1", Stolen: 0, Votes: 4, SeatVotes: 6, Games: 2},
		// game 1: 2 × 1; game 2: 2 × 2, both votes stolen
		"m2": {Key: "m2", Stolen: 2, Votes: 4, SeatVotes: 6, Games: 2},
	}
	if len(fr.ByModel) != 2 {
		t.Fatalf("by model: %+v", fr.ByModel)
	}
	for _, g := range fr.ByModel {
		if g != want[g.Key] {
			t.Errorf("model %s: %+v, want %+v", g.Key, g, want[g.Key])
		}
	}
	wantDiff := []FoolGroup{{Key: "directed", Stolen: 2, Votes: 2, Games: 1}, {Key: "independent", Stolen: 0, Votes: 2, Games: 1}}
	if fmt.Sprint(fr.ByDifficulty) != fmt.Sprint(wantDiff) {
		t.Errorf("by difficulty: %+v", fr.ByDifficulty)
	}
	wantPrompt := []FoolGroup{{Key: "v1", Stolen: 0, Votes: 2, Games: 1}, {Key: "v2", Stolen: 2, Votes: 2, Games: 1}}
	if fmt.Sprint(fr.ByPrompt) != fmt.Sprint(wantPrompt) {
		t.Errorf("by prompt: %+v", fr.ByPrompt)
	}
}

func TestStatsEmpty(t *testing.T) {
	s := testStore(t)
	ctx := context.Background()
	totals, err := s.PlayerTotals(ctx)
	if err != nil || len(totals) != 0 {
		t.Fatalf("%v %v", totals, err)
	}
	fr, err := s.FoolRates(ctx)
	if err != nil || fr.Votes != 0 || fr.ByModel == nil || len(fr.ByModel) != 0 {
		t.Fatalf("%+v %v", fr, err)
	}
}
