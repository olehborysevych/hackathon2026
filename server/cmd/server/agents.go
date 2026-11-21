package main

import (
	"encoding/json"
	"log/slog"
	"math/rand/v2"
	"os"
	"strings"
	"time"
	_ "time/tzdata" // distroless has no zoneinfo; GAME_TZ must still load

	"hackathon2026/server/internal/agent"
	"hackathon2026/server/internal/ai"
	"hackathon2026/server/internal/game"
	"hackathon2026/server/internal/hub"
	"hackathon2026/server/internal/llm"
)

// aiOptions picks the agent for AI seats from env (SPEC §10):
//   - OPENROUTER_API_KEY set (and AI_MODE != stub): real seats via internal/ai, one model per seat
//     from AI_SEAT_MODELS (round-robin), personas from ai.PersonaPool, PROMPT_VERSION (default v2),
//     CRITIC_MODEL (default none), GAME_CONTEXT, GAME_TZ (default Europe/Berlin).
//   - otherwise: the canned stub, as in M2.
//
// bot_human seats always use the stub.
func aiOptions(o hub.Options) hub.Options {
	stub := agent.NewStub(uint64(time.Now().UnixNano()))
	key := os.Getenv("OPENROUTER_API_KEY")
	if key == "" || strings.EqualFold(os.Getenv("AI_MODE"), "stub") {
		if key == "" {
			slog.Warn("OPENROUTER_API_KEY not set: AI seats use the canned stub")
		}
		o.Agent = stub
		return o
	}
	models := listEnv("AI_SEAT_MODELS")
	if len(models) == 0 {
		models = []string{"google/gemma-4-31b-it"}
	}
	for _, m := range models {
		if strings.Contains(strings.ToLower(m), "llama") {
			slog.Error("AI_SEAT_MODELS: Llama models are not allowed (licence forbids presenting outputs as human)", "model", m)
			os.Exit(1)
		}
	}
	version := env("PROMPT_VERSION", "v3")
	if strings.EqualFold(env("DIFFICULTY", "independent"), "directed") {
		slog.Warn("DIFFICULTY=directed is not wired yet: playing independent")
	}
	loc, err := time.LoadLocation(env("GAME_TZ", "Europe/Berlin"))
	if err != nil {
		slog.Warn("GAME_TZ invalid, using server time zone", "err", err)
		loc = nil
	}
	o.Agent = &agent.LLM{
		Client:        llm.NewOpenAICompat(os.Getenv("LLM_BASE_URL"), key),
		CriticModel:   os.Getenv("CRITIC_MODEL"),
		PromptVersion: version,
		GameContext:   env("GAME_CONTEXT", "Hackathon"),
		Fallback:      stub,
		Location:      loc,
		// Intros are optional; humans often skip them (DECISIONS 75).
		IntroExtraSkip: 0.15,
	}
	o.AIProvider, o.PromptVersion, o.Difficulty = "openrouter", version, "independent"
	rng := rand.New(rand.NewPCG(uint64(time.Now().UnixNano()), 11))
	next := 0
	o.AISeats = func(n int) []game.SeatSpec {
		ps := ai.PickPersonas(n, rng) // called under the hub lock, so rng needs no mutex
		out := make([]game.SeatSpec, n)
		for i := range out {
			pj, _ := json.Marshal(ps[i%len(ps)])
			out[i] = game.SeatSpec{Provider: "openrouter", ModelID: models[next%len(models)], Persona: pj}
			next++
		}
		return out
	}
	slog.Info("AI seats: LLM", "models", models, "prompt_version", version, "critic", o.Agent.(*agent.LLM).CriticModel != "")
	return o
}
