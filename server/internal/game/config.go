package game

import (
	"fmt"
	"strconv"
	"strings"
	"time"
)

// Config is the per-game configuration (SPEC §10). Durations are time.Duration so tests can use
// milliseconds; the env loader reads whole seconds.
type Config struct {
	HumansPerGame int
	AIPerGame     int
	Rounds        int

	TIntro  time.Duration
	TAsk    time.Duration
	TAnswer time.Duration
	TReveal time.Duration
	TVote   time.Duration
}

// Text limits (SPEC §2.2, §4), counted in runes.
const (
	MaxIntro  = 160
	MaxAsk    = 160
	MaxAnswer = 200
	MaxNote   = 60
)

func DefaultConfig() Config {
	return Config{
		HumansPerGame: 2, AIPerGame: 3, Rounds: 1,
		TIntro: 60 * time.Second, TAsk: 45 * time.Second, TAnswer: 90 * time.Second,
		TReveal: 30 * time.Second, TVote: 45 * time.Second,
	}
}

func (c Config) Seats() int { return c.HumansPerGame + c.AIPerGame }

func (c Config) Validate() error {
	switch {
	case c.HumansPerGame < 2:
		return fmt.Errorf("HUMANS_PER_GAME must be at least 2")
	case c.AIPerGame < 0:
		return fmt.Errorf("AI_PER_GAME must be 0 or more")
	case c.Seats() > 12:
		return fmt.Errorf("at most 12 seats per game")
	case c.Rounds < 1 || c.Rounds > 3:
		return fmt.Errorf("ROUNDS must be 1–3")
	}
	for name, d := range map[string]time.Duration{
		"T_INTRO": c.TIntro, "T_ASK": c.TAsk, "T_ANSWER": c.TAnswer, "T_REVEAL": c.TReveal, "T_VOTE": c.TVote,
	} {
		if d <= 0 {
			return fmt.Errorf("%s must be positive", name)
		}
	}
	return nil
}

func (c Config) duration(p Phase) time.Duration {
	switch p {
	case Intro:
		return c.TIntro
	case Ask:
		return c.TAsk
	case Answer:
		return c.TAnswer
	case Reveal:
		return c.TReveal
	case Vote:
		return c.TVote
	}
	return 0
}

// ConfigFromEnv reads SPEC §10 variables through getenv (os.Getenv in production). Unset
// variables keep their defaults. T_* are whole seconds and must be at least 5, because AI calls
// get a deadline of phase end − 3s.
func ConfigFromEnv(getenv func(string) string) (Config, error) {
	c := DefaultConfig()
	ints := []struct {
		key string
		dst *int
	}{
		{"HUMANS_PER_GAME", &c.HumansPerGame}, {"AI_PER_GAME", &c.AIPerGame}, {"ROUNDS", &c.Rounds},
	}
	for _, v := range ints {
		if s := strings.TrimSpace(getenv(v.key)); s != "" {
			n, err := strconv.Atoi(s)
			if err != nil {
				return c, fmt.Errorf("%s: %q is not a number", v.key, s)
			}
			*v.dst = n
		}
	}
	durs := []struct {
		key string
		dst *time.Duration
	}{
		{"T_INTRO", &c.TIntro}, {"T_ASK", &c.TAsk}, {"T_ANSWER", &c.TAnswer},
		{"T_REVEAL", &c.TReveal}, {"T_VOTE", &c.TVote},
	}
	for _, v := range durs {
		if s := strings.TrimSpace(getenv(v.key)); s != "" {
			n, err := strconv.Atoi(s)
			if err != nil {
				return c, fmt.Errorf("%s: %q is not a number of seconds", v.key, s)
			}
			if n < 5 {
				return c, fmt.Errorf("%s must be at least 5 seconds", v.key)
			}
			*v.dst = time.Duration(n) * time.Second
		}
	}
	return c, c.Validate()
}
