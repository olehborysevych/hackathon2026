package ai

import (
	"context"
	"fmt"
	"sort"
	"strings"

	"hackathon2026/prompts"
	"hackathon2026/server/internal/llm"
)

// JSON calls (persona, director, analysis) get one retry on invalid output; a second failure is
// returned as an error and the caller falls back (static personas, no instruction, no tactics),
// so the game never waits on them (invariant 6).

type SeatInfo struct {
	Seat  int
	Alias string
	Kind  string // human|ai|bot_human (analysis only; never sent to seats)
}

func jsonCall(ctx context.Context, l llm.LLM, model, purpose, name, version string, kv map[string]string, temp float64,
	validate func(string) error) ([]Call, string, error) {
	tpl, err := prompts.Load(name, version)
	if err != nil {
		return nil, "", err
	}
	msgs := splitPromptCritic(render(tpl, kv))
	var calls []Call
	var lastErr error
	for attempt := 0; attempt < 2; attempt++ {
		out, c, err := complete(ctx, l, llm.CompletionReq{Model: model, Temperature: temp, MaxTokens: 1500, JSON: true, Messages: msgs}, purpose, version)
		calls = append(calls, c)
		if err != nil {
			lastErr = err
			continue
		}
		if err := validate(out); err != nil {
			lastErr = err
			calls[len(calls)-1].Err = "invalid: " + err.Error()
			msgs = append(msgs, llm.Message{Role: "assistant", Content: out},
				llm.Message{Role: "user", Content: "That output was invalid (" + TruncateRunes(err.Error(), 200) + "). Output only the corrected JSON."})
			continue
		}
		return calls, out, nil
	}
	return calls, "", lastErr
}

// GeneratePersonas asks the persona generator for n contrasting personas (directed mode).
func GeneratePersonas(ctx context.Context, l llm.LLM, model, gameContext string, n int) ([]Persona, []Call, error) {
	var ps []Persona
	calls, _, err := jsonCall(ctx, l, model, "persona", "persona", "v1",
		map[string]string{"n": fmt.Sprint(n), "game_context": gameContext}, 1.0,
		func(s string) error { var e error; ps, e = ParsePersonas(s, n); return e })
	return ps, calls, err
}

// DirectRound returns per-seat instructions for the next round.
func DirectRound(ctx context.Context, l llm.LLM, model string, aiSeats []SeatInfo, round int, transcript string) (map[int]string, []string, []Call, error) {
	var nums []int
	var desc []string
	for _, s := range aiSeats {
		nums = append(nums, s.Seat)
		desc = append(desc, fmt.Sprintf("seat %d (%s)", s.Seat, s.Alias))
	}
	var ins map[int]string
	var obs []string
	calls, _, err := jsonCall(ctx, l, model, "director", "director", "v1",
		map[string]string{"ai_seats": strings.Join(desc, ", "), "round": fmt.Sprint(round), "transcript": transcript}, 0.7,
		func(s string) error { var e error; ins, obs, e = ParseDirector(s, nums); return e })
	return ins, obs, calls, err
}

// Analyze runs the post-game analysis. Seats must exclude bot_human (invariant 5): the caller
// passes them, and this function refuses a game that contains one.
func Analyze(ctx context.Context, l llm.LLM, model string, seats []SeatInfo, votes map[int]int, transcript string) ([]Tactic, []Call, error) {
	var aliases, lines []string
	bySeat := map[int]string{}
	for _, s := range seats {
		if s.Kind == "bot_human" {
			return nil, nil, fmt.Errorf("analysis: game has a bot_human seat; excluded from analysis")
		}
		aliases = append(aliases, s.Alias)
		bySeat[s.Seat] = s.Alias
		lines = append(lines, fmt.Sprintf("%s: %s", s.Alias, s.Kind))
	}
	var vl []string
	var voters []int
	for v := range votes {
		voters = append(voters, v)
	}
	sort.Ints(voters)
	for _, v := range voters {
		vl = append(vl, fmt.Sprintf("%s -> %s", bySeat[v], bySeat[votes[v]]))
	}
	var ts []Tactic
	calls, _, err := jsonCall(ctx, l, model, "analysis", "analysis", "v1",
		map[string]string{"seats": strings.Join(lines, "\n"), "votes": strings.Join(vl, "\n"), "transcript": transcript}, 0.3,
		func(s string) error { var e error; ts, _, e = ParseAnalysis(s, aliases); return e })
	return ts, calls, err
}

// Vote asks an AI seat for its (logged, unscored) vote. Falls back to the first candidate.
func Vote(ctx context.Context, l llm.LLM, model, alias string, candidates []string, transcript string) (string, Call, error) {
	tpl, err := prompts.Load("vote", "v1")
	if err != nil {
		return "", Call{}, err
	}
	msgs := splitPromptCritic(render(tpl, map[string]string{"alias": alias, "candidates": strings.Join(candidates, ", "), "transcript": transcript}))
	out, c, err := complete(ctx, l, llm.CompletionReq{Model: model, Temperature: 0.7, MaxTokens: 20, Messages: msgs}, "vote", "v1")
	if err != nil {
		return candidates[0], c, err
	}
	if v, ok := ParseVote(out, candidates); ok {
		return v, c, nil
	}
	c.Err = "unparseable vote"
	return candidates[0], c, fmt.Errorf("vote: unparseable %q", TruncateRunes(out, 60))
}
