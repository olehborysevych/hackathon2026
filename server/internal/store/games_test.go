package store

import (
	"context"
	"math/rand/v2"
	"os"
	"testing"
	"time"

	"hackathon2026/server/internal/agent"
	"hackathon2026/server/internal/game"
)

// Integration tests run against a real Postgres when TEST_DATABASE_URL is set, e.g.
//
//	TEST_DATABASE_URL=postgres://hackathon:hackathon@localhost:5432/hackathon?sslmode=disable go test ./server/internal/store/
//
// The database must be disposable: the test drops and recreates the public schema.
func testStore(t *testing.T) *Store {
	t.Helper()
	url := os.Getenv("TEST_DATABASE_URL")
	if url == "" {
		t.Skip("TEST_DATABASE_URL not set")
	}
	ctx := context.Background()
	s, err := Open(ctx, url)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(s.Close)
	if _, err := s.pool.Exec(ctx, `drop schema public cascade; create schema public`); err != nil {
		t.Fatal(err)
	}
	if err := s.Migrate(ctx); err != nil {
		t.Fatal(err)
	}
	return s
}

// playGame runs a whole game through the state machine and writes every persist record.
func playGame(t *testing.T, s *Store, id string, specs []game.SeatSpec) {
	t.Helper()
	ctx := context.Background()
	g := game.New(id, game.DefaultConfig(), specs, rand.New(rand.NewPCG(uint64(len(id)), uint64(id[len(id)-1])))) // per-game seed: message ids must differ
	now := time.Now()
	write := func(eff []game.Effect) {
		for _, e := range eff {
			p, ok := e.(game.Persist)
			if !ok {
				continue
			}
			var err error
			switch r := p.Record.(type) {
			case game.GameRecord:
				r.Difficulty, r.PromptVersion = "stub", "stub"
				err = s.CreateGame(ctx, r)
			case game.MessageRecord:
				err = s.InsertMessage(ctx, r)
			case game.VoteRecord:
				err = s.InsertVote(ctx, r)
			case game.NoteRecord:
				err = s.InsertNote(ctx, r)
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
		now = now.Add(time.Second)
		switch g.Phase() {
		case game.Intro, game.Ask:
			for _, st := range g.Seats() {
				k := game.MsgIntro
				if g.Phase() == game.Ask {
					k = game.MsgQuestion
				}
				eff, err := g.Submit(now, game.Submission{Seat: st.Index, Phase: g.Phase(), Round: g.Round(), Kind: k, Text: "hi", Skip: st.Index == 0})
				if err != nil {
					t.Fatal(err)
				}
				write(eff)
			}
		case game.Vote:
			eff, err := g.SetNote(now, 0, 1, "human", "gut feeling")
			if err != nil {
				t.Fatal(err)
			}
			write(eff)
			eff, err = g.CastVote(now, 0, 1)
			if err != nil {
				t.Fatal(err)
			}
			write(eff)
			write(g.Tick(g.Deadline()))
		default: // ANSWER left to the deadline: missing answers are stored as skips
			write(g.Tick(g.Deadline()))
		}
	}
	if err := s.InsertAICall(ctx, agent.CallRecord{GameID: id, Seat: 2, Purpose: "intro", Provider: "stub", ModelID: "stub", CreatedAt: now}); err != nil {
		t.Fatal(err)
	}
}

func TestGamePersistenceAndExport(t *testing.T) {
	s := testStore(t)
	ctx := context.Background()
	u1, err := s.CreateUser(ctx, "amber_owl", "x", time.Now())
	if err != nil {
		t.Fatal(err)
	}
	u2, _ := s.CreateUser(ctx, "blue_fox", "x", time.Now())
	ai := game.SeatSpec{Kind: game.KindAI, Provider: "stub", ModelID: "stub"}

	real := "00000000-0000-4000-8000-000000000001"
	withBot := "00000000-0000-4000-8000-000000000002"
	playGame(t, s, real, []game.SeatSpec{{Kind: game.KindHuman, UserID: u1.ID}, {Kind: game.KindHuman, UserID: u2.ID}, ai, ai, ai})
	playGame(t, s, withBot, []game.SeatSpec{{Kind: game.KindHuman, UserID: u1.ID}, {Kind: game.KindBotHuman}, ai, ai, ai})

	var n int
	_ = s.pool.QueryRow(ctx, `select count(*) from messages where game_id=$1`, real).Scan(&n)
	if n != 5+1*(5+4*4) { // intros + one round (questions + 4 asked × 4 answers; seat 0 skips its question)
		t.Fatalf("messages: %d", n)
	}
	_ = s.pool.QueryRow(ctx, `select count(*) from messages where game_id=$1 and skipped and submitted_at is null`, real).Scan(&n)
	if n != 1*16 {
		t.Fatalf("missing answers stored as timed-out skips: %d", n)
	}
	_ = s.pool.QueryRow(ctx, `select count(*) from messages where submitted_at is null and not skipped`).Scan(&n)
	if n != 0 {
		t.Fatal("unsubmitted message not marked skipped")
	}
	var status string
	_ = s.pool.QueryRow(ctx, `select status from games where id=$1`, real).Scan(&status)
	if status != "finished" {
		t.Fatalf("status %s", status)
	}
	for table, want := range map[string]int{"votes": 1, "note_events": 1, "results": 5, "ai_calls": 1, "seats": 5} {
		_ = s.pool.QueryRow(ctx, `select count(*) from `+table+` where game_id=$1`, real).Scan(&n)
		if n != want {
			t.Errorf("%s: %d, want %d", table, n, want)
		}
	}

	// Invariant 5: the bot_human game is not in real_games and not exported.
	_ = s.pool.QueryRow(ctx, `select count(*) from real_games`).Scan(&n)
	if n != 1 {
		t.Fatalf("real_games: %d", n)
	}
	var exported []ExportGame
	if err := s.ExportGames(ctx, func(g ExportGame) error { exported = append(exported, g); return nil }); err != nil {
		t.Fatal(err)
	}
	if len(exported) != 1 || exported[0].ID != real {
		t.Fatalf("exported %d games", len(exported))
	}
	e := exported[0]
	if len(e.Seats) != 5 || len(e.Messages) != 26 || len(e.Votes) != 1 || len(e.Results) != 5 {
		t.Fatalf("export sizes seats=%d messages=%d votes=%d results=%d", len(e.Seats), len(e.Messages), len(e.Votes), len(e.Results))
	}
	if _, ok := e.Seats[0]["user_id"]; !ok {
		t.Fatal("seat export lacks user_id")
	}

	// A restart aborts games that were still running.
	g := game.New("00000000-0000-4000-8000-000000000003", game.DefaultConfig(), []game.SeatSpec{ai, ai}, rand.New(rand.NewPCG(1, 1)))
	for _, eff := range g.Start(time.Now()) {
		if p, ok := eff.(game.Persist); ok {
			if r, ok := p.Record.(game.GameRecord); ok {
				r.Difficulty, r.PromptVersion = "stub", "stub"
				if err := s.CreateGame(ctx, r); err != nil {
					t.Fatal(err)
				}
			}
		}
	}
	if n, err := s.AbortRunningGames(ctx, time.Now()); err != nil || n != 1 {
		t.Fatalf("aborted %d %v", n, err)
	}

	// Deleting an account anonymizes its seats and keeps the game data.
	if _, err := s.pool.Exec(ctx, `delete from users where id=$1`, u2.ID); err != nil {
		t.Fatal(err)
	}
	_ = s.pool.QueryRow(ctx, `select count(*) from seats where game_id=$1 and kind='human' and user_id is null`, real).Scan(&n)
	if n != 1 {
		t.Fatalf("anonymized seats: %d", n)
	}
}
