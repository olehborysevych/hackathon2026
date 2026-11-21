// Package stats serves the leaderboard and AI fool-rate endpoints (SPEC §2.4, §7).
// Usernames appear here and on the stats page only, never in game messages (invariant 3).
// All numbers come from real_games, so bot_human games never count (invariant 5).
package stats

import (
	"context"
	"encoding/json"
	"errors"
	"log/slog"
	"net/http"
	"sort"

	"hackathon2026/server/internal/store"
)

const (
	MinGames  = 5  // games before a player is ranked (SPEC §2.4)
	TopN      = 10 // leaderboard rows shown above the caller's own row (DECISIONS 12)
	RecentN   = 5
	SortDet   = "detection"
	SortRec   = "recognizability"
	SortPts   = "points"
	DefaultBy = SortDet
)

// Store is the persistence the handlers need. *store.Store implements it.
type Store interface {
	PlayerTotals(ctx context.Context) ([]store.PlayerStats, error)
	RecentGames(ctx context.Context, userID string, n int) ([]store.RecentGame, error)
	FoolRates(ctx context.Context) (store.FoolStats, error)
}

// UserIDFunc returns the signed-in user's id (auth.Service.UserID).
type UserIDFunc func(*http.Request) (string, error)

type Service struct {
	store         Store // nil without DATABASE_URL: endpoints answer 503
	userID        UserIDFunc
	humans, seats int
}

// New takes the table size from the game config so the random baselines follow it.
func New(s Store, userID UserIDFunc, humansPerGame, seatsPerGame int) *Service {
	return &Service{store: s, userID: userID, humans: humansPerGame, seats: seatsPerGame}
}

func (s *Service) Routes(mux *http.ServeMux) {
	mux.HandleFunc("GET /api/leaderboard", s.wrap(s.leaderboard))
	mux.HandleFunc("GET /api/stats", s.wrap(s.foolRates))
}

func (s *Service) wrap(h func(http.ResponseWriter, *http.Request, string)) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Cache-Control", "no-store")
		if s.store == nil {
			writeErr(w, http.StatusServiceUnavailable, "Stats are not available (no database configured)")
			return
		}
		uid, err := s.userID(r)
		if err != nil {
			writeErr(w, http.StatusUnauthorized, "Not signed in.")
			return
		}
		h(w, r, uid)
	}
}

// ratio is n/d, 0 when d is 0.
func ratio(n, d int) float64 {
	if d == 0 {
		return 0
	}
	return float64(n) / float64(d)
}

// ---- leaderboard ----

type Row struct {
	Rank            int     `json:"rank,omitempty"` // 0: not ranked yet
	Username        string  `json:"username"`
	Games           int     `json:"games"`
	Points          int     `json:"points"`
	PointsPerGame   float64 `json:"points_per_game"`
	Found           int     `json:"found"`
	WasFound        int     `json:"was_found"`
	Mutual          int     `json:"mutual"`
	Detection       float64 `json:"detection"`
	Recognizability float64 `json:"recognizability"`
	Me              bool    `json:"me,omitempty"`
}

type Me struct {
	Row
	Ranks       map[string]int     `json:"ranks"` // rank on each sort key; empty until ranked
	GamesNeeded int                `json:"games_needed"`
	Recent      []store.RecentGame `json:"recent"`
}

type Leaderboard struct {
	Sort     string  `json:"sort"`
	MinGames int     `json:"min_games"`
	Baseline float64 `json:"baseline"` // chance a random vote finds the other human
	Ranked   int     `json:"ranked"`   // players with ≥ MinGames
	Top      []Row   `json:"top"`
	Me       Me      `json:"me"`
}

func toRow(p store.PlayerStats) Row {
	return Row{
		Username: p.Username, Games: p.Games, Points: p.Points, Found: p.Found, WasFound: p.WasFound, Mutual: p.Mutual,
		PointsPerGame: ratio(p.Points, p.Games), Detection: ratio(p.Found, p.Games), Recognizability: ratio(p.WasFound, p.Games),
	}
}

// metric returns the sort key of p as a fraction num/games, so comparisons stay exact.
func metric(by string, p store.PlayerStats) int {
	switch by {
	case SortRec:
		return p.WasFound
	case SortPts:
		return p.Points
	default:
		return p.Found
	}
}

// cmp orders players best first: the metric rate, then total points (DECISIONS 10), then more
// games. It returns 0 for a true tie, which shares a rank.
func cmp(by string, a, b store.PlayerStats) int {
	l, r := metric(by, a)*b.Games, metric(by, b)*a.Games
	switch {
	case l != r:
		return r - l
	case a.Points != b.Points:
		return b.Points - a.Points
	default:
		return b.Games - a.Games
	}
}

// Rank sorts the players with at least MinGames games best first and returns them with
// competition ranks (1, 1, 3). Ties are listed by username.
func Rank(by string, all []store.PlayerStats) ([]store.PlayerStats, []int) {
	var list []store.PlayerStats
	for _, p := range all {
		if p.Games >= MinGames {
			list = append(list, p)
		}
	}
	sort.SliceStable(list, func(i, j int) bool {
		if c := cmp(by, list[i], list[j]); c != 0 {
			return c < 0
		}
		return list[i].Username < list[j].Username
	})
	ranks := make([]int, len(list))
	for i := range list {
		if i > 0 && cmp(by, list[i-1], list[i]) == 0 {
			ranks[i] = ranks[i-1]
		} else {
			ranks[i] = i + 1
		}
	}
	return list, ranks
}

// Build assembles the response for the caller. Pure, so it is unit-tested without a database.
func Build(by, userID string, all []store.PlayerStats, baseline float64) Leaderboard {
	list, ranks := Rank(by, all)
	lb := Leaderboard{Sort: by, MinGames: MinGames, Baseline: baseline, Ranked: len(list), Top: []Row{}}
	for i, p := range list {
		if i >= TopN {
			break
		}
		row := toRow(p)
		row.Rank, row.Me = ranks[i], p.UserID == userID
		lb.Top = append(lb.Top, row)
	}

	mine := store.PlayerStats{UserID: userID}
	for _, p := range all {
		if p.UserID == userID {
			mine = p
		}
	}
	lb.Me = Me{Row: toRow(mine), Ranks: map[string]int{}, Recent: []store.RecentGame{}}
	lb.Me.Row.Me = true
	if mine.Games < MinGames {
		lb.Me.GamesNeeded = MinGames - mine.Games
		return lb
	}
	for _, key := range []string{SortDet, SortRec, SortPts} {
		l, r := list, ranks
		if key != by {
			l, r = Rank(key, all)
		}
		for i, p := range l {
			if p.UserID == userID {
				lb.Me.Ranks[key] = r[i]
			}
		}
	}
	lb.Me.Rank = lb.Me.Ranks[by]
	return lb
}

func (s *Service) leaderboard(w http.ResponseWriter, r *http.Request, uid string) {
	by := r.URL.Query().Get("sort")
	switch by {
	case "":
		by = DefaultBy
	case SortDet, SortRec, SortPts:
	default:
		writeErr(w, http.StatusBadRequest, "sort must be detection, recognizability or points")
		return
	}
	all, err := s.store.PlayerTotals(r.Context())
	if err != nil {
		serverErr(w, "leaderboard", err)
		return
	}
	// With no real games yet, me.username is empty; the page shows the name from /api/me.
	lb := Build(by, uid, all, ratio(s.humans-1, s.seats-1))
	if lb.Me.Recent, err = s.store.RecentGames(r.Context(), uid, RecentN); err != nil {
		serverErr(w, "recent games", err)
		return
	}
	writeJSON(w, http.StatusOK, lb)
}

// ---- fool rate ----

type Group struct {
	Key      string  `json:"key"`
	Rate     float64 `json:"rate"`
	Stolen   int     `json:"stolen"`
	Votes    int     `json:"votes"` // denominator of Rate
	Games    int     `json:"games"`
	Baseline float64 `json:"baseline"`
}

type FoolRates struct {
	Overall      Group   `json:"overall"`
	PerSeat      Group   `json:"per_seat"` // overall votes per AI seat; same scale as by_model
	ByModel      []Group `json:"by_model"`
	ByDifficulty []Group `json:"by_difficulty"`
	ByPrompt     []Group `json:"by_prompt_version"`
}

// BuildFool turns the store aggregates into rates. Table baselines: a random human vote lands on
// an AI seat with chance AI/(seats−1), and on one given seat with chance 1/(seats−1).
func BuildFool(st store.FoolStats, humans, seats int) FoolRates {
	tableBase := ratio(seats-humans, seats-1)
	seatBase := ratio(1, seats-1)
	group := func(g store.FoolGroup, perSeat bool) Group {
		if perSeat {
			return Group{Key: g.Key, Rate: ratio(g.Stolen, g.SeatVotes), Stolen: g.Stolen, Votes: g.SeatVotes, Games: g.Games, Baseline: seatBase}
		}
		return Group{Key: g.Key, Rate: ratio(g.Stolen, g.Votes), Stolen: g.Stolen, Votes: g.Votes, Games: g.Games, Baseline: tableBase}
	}
	out := FoolRates{
		Overall: Group{Key: "overall", Rate: ratio(st.Stolen, st.Votes), Stolen: st.Stolen, Votes: st.Votes, Games: st.Games, Baseline: tableBase},
		PerSeat: Group{Key: "per_seat", Rate: ratio(st.Stolen, st.AISeatVotes), Stolen: st.Stolen, Votes: st.AISeatVotes, Games: st.Games, Baseline: seatBase},
		ByModel: []Group{}, ByDifficulty: []Group{}, ByPrompt: []Group{},
	}
	for _, g := range st.ByModel {
		out.ByModel = append(out.ByModel, group(g, true))
	}
	for _, g := range st.ByDifficulty {
		out.ByDifficulty = append(out.ByDifficulty, group(g, false))
	}
	for _, g := range st.ByPrompt {
		out.ByPrompt = append(out.ByPrompt, group(g, false))
	}
	return out
}

func (s *Service) foolRates(w http.ResponseWriter, r *http.Request, _ string) {
	st, err := s.store.FoolRates(r.Context())
	if err != nil {
		serverErr(w, "fool rates", err)
		return
	}
	writeJSON(w, http.StatusOK, BuildFool(st, s.humans, s.seats))
}

// ---- helpers (same shape as internal/auth) ----

func writeJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(v)
}

func writeErr(w http.ResponseWriter, status int, msg string) {
	writeJSON(w, status, map[string]string{"error": msg})
}

func serverErr(w http.ResponseWriter, what string, err error) {
	if errors.Is(err, context.Canceled) {
		return
	}
	slog.Error(what, "err", err)
	writeErr(w, http.StatusInternalServerError, "Something went wrong on our side.")
}
