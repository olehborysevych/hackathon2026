package store

import (
	"context"
	"time"
)

// Leaderboard and fool-rate aggregates (SPEC §2.4). Every query reads finished games through
// the real_games view, so games with a bot_human seat never count (CLAUDE.md invariant 5).
// Usernames are read here only for the stats pages; they never reach game messages.

// PlayerStats is one user's totals over real games where they sat as a human.
type PlayerStats struct {
	UserID   string
	Username string
	Games    int
	Points   int
	Found    int // games where this player voted for the other human
	WasFound int // games where the other human voted for this player
	Mutual   int
}

// PlayerTotals returns totals for every user with at least one real game.
func (s *Store) PlayerTotals(ctx context.Context) ([]PlayerStats, error) {
	rows, err := s.pool.Query(ctx, `
		select u.id::text, u.username, count(*)::int, coalesce(sum(r.points), 0)::int,
		       count(*) filter (where r.found_other)::int, count(*) filter (where r.was_found)::int,
		       count(*) filter (where r.mutual)::int
		from real_games g
		join seats s   on s.game_id = g.id and s.kind = 'human'
		join users u   on u.id = s.user_id
		join results r on r.game_id = g.id and r.seat = s.seat
		group by u.id, u.username`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []PlayerStats
	for rows.Next() {
		var p PlayerStats
		if err := rows.Scan(&p.UserID, &p.Username, &p.Games, &p.Points, &p.Found, &p.WasFound, &p.Mutual); err != nil {
			return nil, err
		}
		out = append(out, p)
	}
	return out, rows.Err()
}

// RecentGame is one of a player's own finished real games.
type RecentGame struct {
	GameID   string    `json:"game_id"`
	EndedAt  time.Time `json:"ended_at"`
	Points   int       `json:"points"`
	Found    bool      `json:"found"`
	WasFound bool      `json:"was_found"`
	Mutual   bool      `json:"mutual"`
}

// RecentGames returns the user's last n real games, newest first.
func (s *Store) RecentGames(ctx context.Context, userID string, n int) ([]RecentGame, error) {
	rows, err := s.pool.Query(ctx, `
		select g.id::text, coalesce(g.ended_at, g.started_at), r.points, r.found_other, r.was_found, r.mutual
		from real_games g
		join seats s   on s.game_id = g.id and s.kind = 'human' and s.user_id = $1
		join results r on r.game_id = g.id and r.seat = s.seat
		order by coalesce(g.ended_at, g.started_at) desc
		limit $2`, userID, n)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []RecentGame{}
	for rows.Next() {
		var g RecentGame
		if err := rows.Scan(&g.GameID, &g.EndedAt, &g.Points, &g.Found, &g.WasFound, &g.Mutual); err != nil {
			return nil, err
		}
		out = append(out, g)
	}
	return out, rows.Err()
}

// FoolGroup is the fool rate for one value of a breakdown key.
//
// For difficulty and prompt version: Stolen = human votes on AI seats, Votes = human votes cast,
// both in games with that key (the SPEC §2.3 definition, restricted to those games).
//
// For model: Stolen = human votes on seats played by that model, Votes = human votes cast in
// games where the model sat, SeatVotes = Σ over those games of (human votes × seats of that
// model). Stolen/SeatVotes is the per-seat rate, comparable across models whatever the lineup.
type FoolGroup struct {
	Key       string
	Stolen    int
	Votes     int
	SeatVotes int
	Games     int
}

type FoolStats struct {
	Stolen       int
	Votes        int
	Games        int
	AISeatVotes  int // Σ over games of (human votes × AI seats); Stolen/AISeatVotes is the per-seat rate
	ByModel      []FoolGroup
	ByDifficulty []FoolGroup
	ByPrompt     []FoolGroup
}

// hv is every human vote in a real game, with the target seat's kind and model.
const humanVotes = `
	with hv as (
		select g.id as game_id, g.difficulty, g.prompt_version, t.kind as target_kind, t.model_id as target_model
		from real_games g
		join votes v  on v.game_id = g.id
		join seats vs on vs.game_id = g.id and vs.seat = v.voter_seat and vs.kind = 'human'
		join seats t  on t.game_id = g.id and t.seat = v.target_seat
	)`

func (s *Store) FoolRates(ctx context.Context) (FoolStats, error) {
	var st FoolStats
	err := s.pool.QueryRow(ctx, humanVotes+`,
		per_game as (select game_id, count(*) as n from hv group by game_id),
		ai_seats as (select game_id, count(*) as n from seats where kind = 'ai' group by game_id)
		select (select count(*) filter (where target_kind = 'ai') from hv)::int,
		       (select count(*) from hv)::int,
		       (select count(*) from per_game)::int,
		       (select coalesce(sum(p.n * coalesce(a.n, 0)), 0) from per_game p left join ai_seats a using (game_id))::int`).
		Scan(&st.Stolen, &st.Votes, &st.Games, &st.AISeatVotes)
	if err != nil {
		return st, err
	}

	if st.ByDifficulty, err = s.foolGroups(ctx, humanVotes+`
		select difficulty, count(*) filter (where target_kind = 'ai')::int, count(*)::int, 0, count(distinct game_id)::int
		from hv group by difficulty order by difficulty`); err != nil {
		return st, err
	}
	if st.ByPrompt, err = s.foolGroups(ctx, humanVotes+`
		select prompt_version, count(*) filter (where target_kind = 'ai')::int, count(*)::int, 0, count(distinct game_id)::int
		from hv group by prompt_version order by prompt_version`); err != nil {
		return st, err
	}
	st.ByModel, err = s.foolGroups(ctx, humanVotes+`,
		per_game as (select game_id, count(*) as n from hv group by game_id),
		model_seats as (
			select s.game_id, coalesce(s.model_id, 'unknown') as model, count(*) as seats
			from seats s join per_game p using (game_id)
			where s.kind = 'ai' group by 1, 2),
		stolen as (
			select game_id, coalesce(target_model, 'unknown') as model, count(*) as n
			from hv where target_kind = 'ai' group by 1, 2)
		select m.model, coalesce(sum(st.n), 0)::int, sum(p.n)::int, sum(p.n * m.seats)::int, count(*)::int
		from model_seats m
		join per_game p using (game_id)
		left join stolen st on st.game_id = m.game_id and st.model = m.model
		group by m.model order by m.model`)
	return st, err
}

func (s *Store) foolGroups(ctx context.Context, sql string) ([]FoolGroup, error) {
	rows, err := s.pool.Query(ctx, sql)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []FoolGroup{}
	for rows.Next() {
		var g FoolGroup
		if err := rows.Scan(&g.Key, &g.Stolen, &g.Votes, &g.SeatVotes, &g.Games); err != nil {
			return nil, err
		}
		out = append(out, g)
	}
	return out, rows.Err()
}
