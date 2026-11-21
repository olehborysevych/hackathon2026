package store

import (
	"context"
	"encoding/json"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5"

	"hackathon2026/server/internal/agent"
	"hackathon2026/server/internal/game"
)

// configJSON is the stored form of game.Config: durations in whole seconds.
type configJSON struct {
	HumansPerGame int     `json:"humans_per_game"`
	AIPerGame     int     `json:"ai_per_game"`
	Rounds        int     `json:"rounds"`
	TIntro        float64 `json:"t_intro"`
	TAsk          float64 `json:"t_ask"`
	TAnswer       float64 `json:"t_answer"`
	TReveal       float64 `json:"t_reveal"`
	TVote         float64 `json:"t_vote"`
}

func nullable(s string) *string {
	if s == "" {
		return nil
	}
	return &s
}

// nullableJSON passes JSON as text so pgx sends it to a jsonb column; empty = NULL.
func nullableJSON(b []byte) *string {
	if len(b) == 0 {
		return nil
	}
	s := string(b)
	return &s
}

func nullableInt(n int) *int {
	if n == 0 {
		return nil
	}
	return &n
}

func nullableFloat(f float64) *float64 {
	if f == 0 {
		return nil
	}
	return &f
}

// CreateGame stores the game and its seats in one transaction.
func (s *Store) CreateGame(ctx context.Context, r game.GameRecord) error {
	c := r.Config
	cfg, err := json.Marshal(configJSON{
		HumansPerGame: c.HumansPerGame, AIPerGame: c.AIPerGame, Rounds: c.Rounds,
		TIntro: c.TIntro.Seconds(), TAsk: c.TAsk.Seconds(), TAnswer: c.TAnswer.Seconds(),
		TReveal: c.TReveal.Seconds(), TVote: c.TVote.Seconds(),
	})
	if err != nil {
		return err
	}
	return pgx.BeginFunc(ctx, s.pool, func(tx pgx.Tx) error {
		if _, err := tx.Exec(ctx,
			`insert into games(id, difficulty, config, prompt_version, status, started_at)
			 values($1,$2,$3,$4,'running',$5)`,
			r.ID, r.Difficulty, cfg, r.PromptVersion, r.StartedAt); err != nil {
			return fmt.Errorf("insert game: %w", err)
		}
		for _, st := range r.Seats {
			if _, err := tx.Exec(ctx,
				`insert into seats(game_id, seat, alias, sprite_seed, kind, user_id, provider, model_id, persona)
				 values($1,$2,$3,$4,$5,$6,$7,$8,$9)`,
				r.ID, st.Index, st.Alias, st.SpriteSeed, string(st.Kind),
				nullable(st.UserID), nullable(st.Provider), nullable(st.ModelID), nullableJSON(st.Persona)); err != nil {
				return fmt.Errorf("insert seat %d: %w", st.Index, err)
			}
		}
		return nil
	})
}

func (s *Store) InsertMessage(ctx context.Context, r game.MessageRecord) error {
	var text *string
	if !r.Skipped {
		text = &r.Text
	}
	_, err := s.pool.Exec(ctx,
		`insert into messages(id, game_id, round, phase, kind, seat, question_id, text, skipped,
		   phase_started_at, submitted_at, first_keystroke_ms, edit_count, broke_character, out_of_game)
		 values($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11,$12,$13,$14,$15)`,
		r.ID, r.GameID, r.Round, string(r.Phase), string(r.Kind), r.Seat, nullable(r.QuestionID), text, r.Skipped,
		r.PhaseStartedAt, r.SubmittedAt, r.FirstKeystrokeMs, r.EditCount, r.BrokeCharacter, r.OutOfGame)
	return err
}

func (s *Store) InsertVote(ctx context.Context, r game.VoteRecord) error {
	_, err := s.pool.Exec(ctx,
		`insert into votes(game_id, voter_seat, target_seat, cast_at) values($1,$2,$3,$4)`,
		r.GameID, r.Voter, r.Target, r.CastAt)
	return err
}

func (s *Store) InsertNote(ctx context.Context, r game.NoteRecord) error {
	_, err := s.pool.Exec(ctx,
		`insert into note_events(game_id, owner_seat, target_seat, mark, note, round, phase, ts)
		 values($1,$2,$3,$4,$5,$6,$7,$8)`,
		r.GameID, r.Owner, r.Target, r.Mark, nullable(r.Note), r.Round, string(r.Phase), r.TS)
	return err
}

// FinishGame stores the results and marks the game finished.
func (s *Store) FinishGame(ctx context.Context, r game.FinishRecord) error {
	return pgx.BeginFunc(ctx, s.pool, func(tx pgx.Tx) error {
		for _, res := range r.Results {
			if _, err := tx.Exec(ctx,
				`insert into results(game_id, seat, points, found_other, was_found, mutual, votes_stolen)
				 values($1,$2,$3,$4,$5,$6,$7)`,
				r.GameID, res.Seat, res.Points, res.FoundOther, res.WasFound, res.Mutual, res.VotesStolen); err != nil {
				return err
			}
		}
		_, err := tx.Exec(ctx, `update games set status='finished', ended_at=$2 where id=$1`, r.GameID, r.EndedAt)
		return err
	})
}

func (s *Store) InsertAICall(ctx context.Context, r agent.CallRecord) error {
	_, err := s.pool.Exec(ctx,
		`insert into ai_calls(game_id, seat, purpose, provider, model_id, prompt_version, request, draft, final,
		   latency_ms, input_tokens, output_tokens, cost_usd, error, created_at)
		 values($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11,$12,$13,$14,$15)`,
		r.GameID, r.Seat, r.Purpose, nullable(r.Provider), nullable(r.ModelID), nullable(r.PromptVersion),
		nullableJSON(r.Request), nullable(r.Draft), nullable(r.Final), r.LatencyMs,
		nullableInt(r.InputTokens), nullableInt(r.OutputTokens), nullableFloat(r.CostUSD), nullable(r.Error), r.CreatedAt)
	return err
}

// AbortRunningGames marks games left running by a previous process as aborted. In-memory game
// state does not survive a restart (SPEC §5).
func (s *Store) AbortRunningGames(ctx context.Context, now time.Time) (int64, error) {
	tag, err := s.pool.Exec(ctx, `update games set status='aborted', ended_at=$1 where status='running'`, now)
	return tag.RowsAffected(), err
}

// ---- export (SPEC §6: JSONL per game). Reads real_games only, so bot_human games never leave. ----

type ExportGame struct {
	ID            string           `json:"id"`
	Mode          string           `json:"mode"`
	Difficulty    string           `json:"difficulty"`
	PromptVersion string           `json:"prompt_version"`
	Config        json.RawMessage  `json:"config"`
	StartedAt     time.Time        `json:"started_at"`
	EndedAt       *time.Time       `json:"ended_at"`
	Seats         []map[string]any `json:"seats"`
	Messages      []map[string]any `json:"messages"`
	Votes         []map[string]any `json:"votes"`
	Notes         []map[string]any `json:"notes"`
	Results       []map[string]any `json:"results"`
	AICalls       []map[string]any `json:"ai_calls"`
}

// ExportGames calls fn for each finished game without bot_human seats, oldest first.
func (s *Store) ExportGames(ctx context.Context, fn func(ExportGame) error) error {
	rows, err := s.pool.Query(ctx,
		`select id::text, mode, difficulty, prompt_version, config, started_at, ended_at from real_games order by started_at`)
	if err != nil {
		return err
	}
	var games []ExportGame
	for rows.Next() {
		var g ExportGame
		if err := rows.Scan(&g.ID, &g.Mode, &g.Difficulty, &g.PromptVersion, &g.Config, &g.StartedAt, &g.EndedAt); err != nil {
			rows.Close()
			return err
		}
		games = append(games, g)
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return err
	}
	for _, g := range games {
		parts := []struct {
			dst *[]map[string]any
			sql string
		}{
			{&g.Seats, `select seat, alias, sprite_seed, kind, user_id::text as user_id, provider, model_id, persona from seats where game_id=$1 order by seat`},
			{&g.Messages, `select id::text as id, round, phase, kind, seat, question_id::text as question_id, text, skipped, phase_started_at, submitted_at,
			   first_keystroke_ms, edit_count, broke_character, out_of_game from messages where game_id=$1 order by phase_started_at, submitted_at nulls last, seat`},
			{&g.Votes, `select voter_seat, target_seat, cast_at from votes where game_id=$1 order by cast_at`},
			{&g.Notes, `select owner_seat, target_seat, mark, note, round, phase, ts from note_events where game_id=$1 order by id`},
			{&g.Results, `select seat, points, found_other, was_found, mutual, votes_stolen from results where game_id=$1 order by seat`},
			{&g.AICalls, `select seat, purpose, provider, model_id, prompt_version, draft, final, latency_ms, input_tokens,
			   output_tokens, cost_usd::float8 as cost_usd, error, created_at from ai_calls where game_id=$1 order by created_at`},
		}
		for _, p := range parts {
			rows, err := s.pool.Query(ctx, p.sql, g.ID)
			if err != nil {
				return err
			}
			out, err := pgx.CollectRows(rows, pgx.RowToMap)
			if err != nil {
				return err
			}
			if out == nil {
				out = []map[string]any{}
			}
			*p.dst = out
		}
		if err := fn(g); err != nil {
			return err
		}
	}
	return nil
}
