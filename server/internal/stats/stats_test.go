package stats

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"

	"hackathon2026/server/internal/game"
	"hackathon2026/server/internal/store"
	"hackathon2026/server/internal/ws"
)

func player(name string, games, points, found, wasFound int) store.PlayerStats {
	return store.PlayerStats{UserID: "id-" + name, Username: name, Games: games, Points: points, Found: found, WasFound: wasFound}
}

func TestRankOrderTiesAndThreshold(t *testing.T) {
	all := []store.PlayerStats{
		player("newbie", 4, 12, 4, 4), // 100% but unranked
		player("carol", 10, 9, 5, 2),  // 50%
		player("dave", 5, 4, 3, 1),    // 60%
		player("erin", 10, 9, 5, 4),   // 50%, same points as carol: tie
		player("frank", 6, 10, 3, 3),  // 50%, more points: ahead of carol/erin
		player("gina", 20, 8, 2, 15),  // 10%, was found 75%
	}
	list, ranks := Rank(SortDet, all)
	var got []string
	for i, p := range list {
		got = append(got, fmt.Sprintf("%d:%s", ranks[i], p.Username))
	}
	want := "[1:dave 2:frank 3:carol 3:erin 5:gina]"
	if fmt.Sprint(got) != want {
		t.Fatalf("got %v want %s", got, want)
	}

	list, _ = Rank(SortRec, all)
	if list[0].Username != "gina" {
		t.Fatalf("recognizability leader %s", list[0].Username)
	}
	list, _ = Rank(SortPts, all) // frank 10/6 = 1.67 per game
	if list[0].Username != "frank" {
		t.Fatalf("points leader %s", list[0].Username)
	}
}

func TestBuildTopNAndOwnRow(t *testing.T) {
	var all []store.PlayerStats
	for i := 0; i < 15; i++ { // p00 best ... p14 worst
		all = append(all, player(fmt.Sprintf("p%02d", i), 10, 0, 10-i/2, i%3))
	}
	lb := Build(SortDet, "id-p13", all, 0.25)
	if len(lb.Top) != TopN || lb.Ranked != 15 || lb.Baseline != 0.25 {
		t.Fatalf("top %d ranked %d", len(lb.Top), lb.Ranked)
	}
	if lb.Me.Username != "p13" || lb.Me.Rank != 13 || lb.Me.GamesNeeded != 0 || !lb.Me.Me {
		t.Fatalf("me %+v", lb.Me)
	}
	if len(lb.Me.Ranks) != 3 || lb.Me.Ranks[SortDet] != 13 {
		t.Fatalf("ranks %+v", lb.Me.Ranks)
	}
	for _, r := range lb.Top {
		if r.Me {
			t.Fatal("caller is outside the top 10 but marked in it")
		}
	}

	in := Build(SortDet, "id-p00", all, 0.25)
	if !in.Top[0].Me || in.Me.Rank != 1 {
		t.Fatalf("leader %+v", in.Top[0])
	}

	// Unranked: no rank, games still needed. No games at all: zeros.
	all = append(all, player("rookie", 2, 3, 1, 1))
	r := Build(SortDet, "id-rookie", all, 0.25)
	if r.Me.Rank != 0 || r.Me.GamesNeeded != 3 || len(r.Me.Ranks) != 0 || r.Me.Detection != 0.5 {
		t.Fatalf("rookie %+v", r.Me)
	}
	z := Build(SortPts, "nobody", all, 0.25)
	if z.Me.Games != 0 || z.Me.GamesNeeded != MinGames || z.Me.Detection != 0 {
		t.Fatalf("nobody %+v", z.Me)
	}
}

func TestBuildFoolBaselines(t *testing.T) {
	fr := BuildFool(store.FoolStats{
		Stolen: 3, Votes: 4, AISeatVotes: 12, Games: 2,
		ByModel:      []store.FoolGroup{{Key: "stub", Stolen: 3, Votes: 4, SeatVotes: 12, Games: 2}},
		ByDifficulty: []store.FoolGroup{{Key: "stub", Stolen: 3, Votes: 4, Games: 2}},
	}, 2, 5)
	if fr.Overall.Rate != 0.75 || fr.Overall.Baseline != 0.75 {
		t.Fatalf("overall %+v", fr.Overall)
	}
	if fr.PerSeat.Rate != 0.25 || fr.PerSeat.Baseline != 0.25 {
		t.Fatalf("per seat %+v", fr.PerSeat)
	}
	if m := fr.ByModel[0]; m.Rate != 0.25 || m.Votes != 12 || m.Baseline != 0.25 {
		t.Fatalf("model %+v", m)
	}
	if d := fr.ByDifficulty[0]; d.Rate != 0.75 || d.Baseline != 0.75 {
		t.Fatalf("difficulty %+v", d)
	}
	if fr.ByPrompt == nil {
		t.Fatal("empty groups must encode as [] not null")
	}
}

// ---- handlers ----

type fakeStore struct{ totals []store.PlayerStats }

func (f fakeStore) PlayerTotals(context.Context) ([]store.PlayerStats, error) { return f.totals, nil }
func (f fakeStore) RecentGames(context.Context, string, int) ([]store.RecentGame, error) {
	return []store.RecentGame{}, nil
}
func (f fakeStore) FoolRates(context.Context) (store.FoolStats, error) { return store.FoolStats{}, nil }

func signedIn(id string) UserIDFunc {
	return func(*http.Request) (string, error) {
		if id == "" {
			return "", errors.New("no session")
		}
		return id, nil
	}
}

func get(t *testing.T, s *Service, path string, v any) int {
	t.Helper()
	mux := http.NewServeMux()
	s.Routes(mux)
	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, path, nil))
	if v != nil && rec.Code == http.StatusOK {
		if err := json.Unmarshal(rec.Body.Bytes(), v); err != nil {
			t.Fatal(err)
		}
	}
	return rec.Code
}

func TestHandlers(t *testing.T) {
	if c := get(t, New(nil, signedIn("u"), 2, 5), "/api/leaderboard", nil); c != http.StatusServiceUnavailable {
		t.Fatalf("no db: %d", c)
	}
	fs := fakeStore{totals: []store.PlayerStats{player("amy", 5, 5, 2, 3)}}
	for _, p := range []string{"/api/leaderboard", "/api/stats"} {
		if c := get(t, New(fs, signedIn(""), 2, 5), p, nil); c != http.StatusUnauthorized {
			t.Fatalf("%s signed out: %d", p, c)
		}
	}
	svc := New(fs, signedIn("id-amy"), 2, 5)
	if c := get(t, svc, "/api/leaderboard?sort=nope", nil); c != http.StatusBadRequest {
		t.Fatalf("bad sort: %d", c)
	}
	var lb Leaderboard
	if c := get(t, svc, "/api/leaderboard", &lb); c != http.StatusOK || lb.Sort != SortDet || lb.Baseline != 0.25 || lb.Me.Rank != 1 {
		t.Fatalf("%d %+v", c, lb)
	}
	if c := get(t, New(fs, signedIn("id-amy"), 3, 6), "/api/leaderboard?sort=points", &lb); c != http.StatusOK || lb.Baseline != 0.4 || lb.Sort != SortPts {
		t.Fatalf("3 humans of 6: %+v", lb)
	}
}

// ---- end to end against Postgres: both endpoints ignore bot_human games (invariant 5) ----

func testDB(t *testing.T) *store.Store {
	t.Helper()
	url := os.Getenv("TEST_DATABASE_URL")
	if url == "" {
		t.Skip("TEST_DATABASE_URL not set")
	}
	ctx := context.Background()
	conn, err := pgx.Connect(ctx, url)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := conn.Exec(ctx, `drop schema public cascade; create schema public`); err != nil {
		t.Fatal(err)
	}
	conn.Close(ctx)
	s, err := store.Open(ctx, url)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(s.Close)
	if err := s.Migrate(ctx); err != nil {
		t.Fatal(err)
	}
	return s
}

// finishedGame stores a game with fixed seats (no shuffle), the given votes and the given results.
func finishedGame(t *testing.T, s *store.Store, n int, seats []game.SeatSpec, votes map[int]int, results []ws.SeatResult) {
	t.Helper()
	ctx := context.Background()
	id := fmt.Sprintf("00000000-0000-4000-8000-%012d", n)
	rec := game.GameRecord{ID: id, Config: game.DefaultConfig(), Difficulty: "stub", PromptVersion: "stub", StartedAt: time.Now()}
	for i, sp := range seats {
		rec.Seats = append(rec.Seats, game.Seat{Index: i, Alias: fmt.Sprintf("Seat %d", i), SpriteSeed: i, SeatSpec: sp})
	}
	if err := s.CreateGame(ctx, rec); err != nil {
		t.Fatal(err)
	}
	for v, tg := range votes {
		if err := s.InsertVote(ctx, game.VoteRecord{GameID: id, Voter: v, Target: tg, CastAt: time.Now()}); err != nil {
			t.Fatal(err)
		}
	}
	if err := s.FinishGame(ctx, game.FinishRecord{GameID: id, EndedAt: time.Now(), Results: results}); err != nil {
		t.Fatal(err)
	}
}

func TestEndpointsExcludeBotHumanGames(t *testing.T) {
	s := testDB(t)
	ctx := context.Background()
	amy, _ := s.CreateUser(ctx, "amy", "x", time.Now())
	ben, _ := s.CreateUser(ctx, "ben", "x", time.Now())
	ai := game.SeatSpec{Kind: game.KindAI, Provider: "stub", ModelID: "stub"}
	zero := 0
	one := 1

	// Real game: amy and ben miss each other, both vote seat 2 (AI).
	two := 2
	finishedGame(t, s, 1, []game.SeatSpec{{Kind: game.KindHuman, UserID: amy.ID}, {Kind: game.KindHuman, UserID: ben.ID}, ai, ai, ai},
		map[int]int{0: 2, 1: 2},
		[]ws.SeatResult{{Seat: 0}, {Seat: 1}, {Seat: 2, VotesStolen: &two}, {Seat: 3, VotesStolen: &zero}, {Seat: 4, VotesStolen: &zero}})
	// Dev game with a bot_human: a mutual match worth 3 points, and no stolen votes. Must not count.
	for i := 0; i < 6; i++ {
		finishedGame(t, s, 10+i, []game.SeatSpec{{Kind: game.KindHuman, UserID: amy.ID}, {Kind: game.KindBotHuman}, ai, ai, ai},
			map[int]int{0: 1, 1: 0},
			[]ws.SeatResult{{Seat: 0, Points: 3, FoundOther: true, WasFound: true, Mutual: true}, {Seat: 1, Points: 3, FoundOther: true, WasFound: true, Mutual: true},
				{Seat: 2, VotesStolen: &zero}, {Seat: 3, VotesStolen: &zero}, {Seat: 4, VotesStolen: &one}})
	}

	svc := New(s, signedIn(amy.ID), 2, 5)
	var lb Leaderboard
	if c := get(t, svc, "/api/leaderboard", &lb); c != http.StatusOK {
		t.Fatalf("leaderboard %d", c)
	}
	// With the 6 bot games amy would have 7 games, be ranked and have 18 points.
	if lb.Me.Games != 1 || lb.Me.Points != 0 || lb.Me.GamesNeeded != 4 || lb.Ranked != 0 || len(lb.Top) != 0 || len(lb.Me.Recent) != 1 {
		t.Fatalf("leaderboard counted bot_human games: %+v", lb)
	}

	var fr FoolRates
	if c := get(t, svc, "/api/stats", &fr); c != http.StatusOK {
		t.Fatalf("stats %d", c)
	}
	if fr.Overall.Votes != 2 || fr.Overall.Stolen != 2 || fr.Overall.Games != 1 || fr.Overall.Rate != 1 {
		t.Fatalf("stats counted bot_human games: %+v", fr.Overall)
	}
	if len(fr.ByModel) != 1 || fr.ByModel[0].Key != "stub" || fr.ByModel[0].Votes != 6 {
		t.Fatalf("by model: %+v", fr.ByModel)
	}
}
