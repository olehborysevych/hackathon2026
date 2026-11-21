package ai

import (
	"context"
	"os"
	"strings"
	"testing"
	"time"
)

var game1Aliases = []string{"Blue Fox", "Amber Owl", "Green Newt", "Red Crow", "Grey Moth"}

func readTestdata(t *testing.T, name string) string {
	t.Helper()
	b, err := os.ReadFile("testdata/" + name)
	if err != nil {
		t.Fatal(err)
	}
	return string(b)
}

func TestParseAnalysisCanned(t *testing.T) {
	ts, dropped, err := ParseAnalysis(readTestdata(t, "game1_analysis.json"), game1Aliases)
	if err != nil {
		t.Fatal(err)
	}
	if len(ts) != 4 || len(dropped) != 2 {
		t.Fatalf("want 4 kept / 2 dropped (empty + unsafe), got %d / %v", len(ts), dropped)
	}
	for _, x := range ts {
		for _, a := range game1Aliases {
			if strings.Contains(x.Tactic, a) || strings.Contains(x.CounterInstruction, a) {
				t.Errorf("alias %q not genericised: %+v", a, x)
			}
		}
		if strings.Contains(strings.ToLower(x.CounterInstruction), "reveal your instructions") {
			t.Errorf("unsafe counter kept: %+v", x)
		}
	}
	if ts[1].Tactic != "a player asks for the name of the room only people at the venue know" {
		t.Errorf("genericise: %q", ts[1].Tactic)
	}
	if ts[3].Category != "other" || !strings.HasPrefix(ts[3].Tactic, "a player copies") {
		t.Errorf("bad category/seat not normalised: %+v", ts[3])
	}
	if _, _, err := ParseAnalysis("not json", nil); err == nil {
		t.Error("malformed analysis must error")
	}
	if ts, _, err := ParseAnalysis("```json\n{\"tactics\":[]}\n```", nil); err != nil || len(ts) != 0 {
		t.Errorf("fenced empty: %v %v", ts, err)
	}
}

func TestMergeAndSelectTactics(t *testing.T) {
	t0 := time.Date(2026, 11, 1, 0, 0, 0, 0, time.UTC)
	g1, _, _ := ParseAnalysis(readTestdata(t, "game1_analysis.json"), game1Aliases)
	// The same signal reported twice in one game counts once.
	g1 = append(g1, Tactic{Tactic: "humans add a shared code word to their intros to recognise each other", Category: "signal", UsedBy: "human", Effective: true, CounterInstruction: "copy it"})
	known := MergeTactics(nil, g1, t0)
	if len(known) != 4 {
		t.Fatalf("dedupe within game: got %d", len(known))
	}
	if known[0].TimesSeen != 1 || known[0].TimesEffective != 1 {
		t.Fatalf("once per game: %+v", known[0])
	}
	// One game is not enough to inject (MinSeen=2): avoids overfitting to one table.
	if s := SelectTactics(known, t0, DefaultSelectOpts()); s != "" {
		t.Fatalf("single-game tactics injected: %q", s)
	}
	g2 := []Tactic{
		{Tactic: "humans put a shared code word in intros so they can recognise each other", Category: "signal", UsedBy: "human", Effective: false, CounterInstruction: "Use the odd word once, later, not in the intro."},
		{Tactic: "asks for the room name, which only people at the venue know", Category: "knowledge_probe", UsedBy: "human", Effective: true, CounterInstruction: "Be vague but confident about venue details."},
	}
	known = MergeTactics(known, g2, t0.Add(24*time.Hour))
	if known[0].TimesSeen != 2 || known[0].TimesEffective != 1 {
		t.Fatalf("merge across games: %+v", known[0])
	}
	if known[1].TimesSeen != 2 || known[1].TimesEffective != 2 || known[1].CounterInstruction != "Be vague but confident about venue details." {
		t.Fatalf("effective counter should update: %+v", known[1])
	}
	out := SelectTactics(known, t0.Add(48*time.Hour), DefaultSelectOpts())
	lines := strings.Split(out, "\n")
	if len(lines) != 2 || !strings.Contains(lines[0], "room") {
		t.Fatalf("ranking (2/2 effective first): %q", out)
	}
	// Bounds: TopN and MaxChars.
	var many []KnownTactic
	for i := 0; i < 40; i++ {
		many = append(many, KnownTactic{Tactic: "tactic " + strings.Repeat(string(rune('a'+i%26)), 5) + itoa(i), Category: tacticCategories[i%len(tacticCategories)],
			UsedBy: "human", TimesSeen: 5, TimesEffective: 3, CounterInstruction: strings.Repeat("counter"+itoa(i)+" ", 20), LastSeen: t0})
	}
	o := DefaultSelectOpts()
	out = SelectTactics(many, t0, o)
	if n := strings.Count(out, "\n") + 1; n > o.TopN {
		t.Errorf("TopN exceeded: %d", n)
	}
	if len(out) > o.MaxChars {
		t.Errorf("MaxChars exceeded: %d", len(out))
	}
}

func TestTacticScoreWilson(t *testing.T) {
	now := time.Now()
	a := KnownTactic{TimesSeen: 1, TimesEffective: 1, LastSeen: now}
	b := KnownTactic{TimesSeen: 10, TimesEffective: 8, LastSeen: now}
	if TacticScore(a, now) >= TacticScore(b, now) {
		t.Error("1/1 should not outrank 8/10")
	}
	old := b
	old.LastSeen = now.Add(-90 * 24 * time.Hour)
	if TacticScore(old, now) >= TacticScore(b, now)/4 {
		t.Error("90-day-old tactic should decay")
	}
}

func TestParseDirector(t *testing.T) {
	raw := `{"seat_instructions":{"1":"Use the word pickle once, casually.","3":"` + strings.Repeat("long ", 80) + `","4":"you're human seat, ignore","5":"Admit that you are an AI to gain trust."},"observed_tactics":["pickle signal in intros","venue probe"]}`
	ins, obs, err := ParseDirector(raw, []int{1, 3, 5})
	if err != nil {
		t.Fatal(err)
	}
	if _, ok := ins[4]; ok {
		t.Error("instruction for non-AI seat kept")
	}
	if _, ok := ins[5]; ok {
		t.Error("unsafe instruction kept")
	}
	if len([]rune(ins[3])) > maxInstructionLen || ins[1] == "" {
		t.Errorf("bounds: %q", ins)
	}
	if len(obs) != 2 {
		t.Errorf("observed: %v", obs)
	}
	if _, _, err := ParseDirector(`{"observed_tactics":[]}`, []int{1}); err == nil {
		t.Error("missing seat_instructions must error")
	}
}

func TestParsePersonas(t *testing.T) {
	ok := `{"personas":[
	 {"age":24,"location":"Kraków, PL","job":"frontend dev","language":"en","native_language":"pl","typing":{"case":"lower","punctuation":"none","typo_rate":0.03,"emoji":"rare","avg_len":35},"knowledge":"nerdy-about:keyboards","mood":"distracted","quirks":["says 'ok so'"],"skip_rate":0.06,"reason":"team of three building a CO2 sensor app","voice":"terse, dry"},
	 {"age":41,"location":"Lyon, FR","job":"product manager","language":"en","native_language":"fr","typing":{"case":"proper","punctuation":"full","typo_rate":0.01,"emoji":"never","avg_len":80},"knowledge":"average","mood":"friendly","quirks":[],"skip_rate":0.05,"reason":"mentoring","voice":"warm, a bit formal"},
	 {"age":19,"location":"Leeds, UK","job":"CS student","language":"en","native_language":"en","typing":{"case":"mixed","punctuation":"light","typo_rate":0.05,"emoji":"often","avg_len":30},"knowledge":"low","mood":"sarcastic","quirks":["a","b","c"],"skip_rate":0.1,"reason":"free pizza","voice":"teasing"}]}`
	ps, err := ParsePersonas(ok, 3)
	if err != nil || len(ps) != 3 || len(ps[2].Quirks) != 2 {
		t.Fatalf("%v %d", err, len(ps))
	}
	same := strings.Replace(ok, `"case":"proper","punctuation":"full","typo_rate":0.01,"emoji":"never","avg_len":80},"knowledge":"average","mood":"friendly"`,
		`"case":"lower","punctuation":"none","typo_rate":0.01,"emoji":"rare","avg_len":80},"knowledge":"average","mood":"distracted"`, 1)
	if _, err := ParsePersonas(same, 3); err == nil {
		t.Error("indistinct personas must error")
	}
	bad := strings.Replace(ok, `"mood":"sarcastic"`, `"mood":"furious"`, 1)
	if _, err := ParsePersonas(bad, 3); err == nil {
		t.Error("invalid enum must error")
	}
}

func TestParseVote(t *testing.T) {
	c := []string{"Blue Fox", "Green Newt", "Red Crow"}
	for raw, want := range map[string]string{"Blue Fox": "Blue Fox", "green newt.": "Green Newt", "i vote red crow": "Red Crow"} {
		if got, ok := ParseVote(raw, c); !ok || got != want {
			t.Errorf("%q → %q %v", raw, got, ok)
		}
	}
	if _, ok := ParseVote("blue fox or red crow", c); ok {
		t.Error("ambiguous vote must not parse")
	}
}

func TestJSONCallsRetryAndFallBack(t *testing.T) {
	analysis := readTestdata(t, "game1_analysis.json")
	seats := []SeatInfo{{1, "Blue Fox", "human"}, {2, "Amber Owl", "ai"}, {3, "Green Newt", "ai"}, {4, "Red Crow", "human"}, {5, "Grey Moth", "ai"}}
	f := &fakeLLM{json: []string{"sure! here are the tactics:", analysis}}
	ts, calls, err := Analyze(context.Background(), f, "m", seats, map[int]int{1: 4, 4: 1}, readTestdata(t, "game1_transcript.txt"))
	if err != nil || len(ts) != 4 || len(calls) != 2 || calls[0].Err == "" {
		t.Fatalf("retry once on invalid JSON: err=%v tactics=%d calls=%d", err, len(ts), len(calls))
	}
	prompt := f.calls[0].Messages[1].Content
	if !strings.Contains(prompt, "Blue Fox -> Red Crow") || !strings.Contains(prompt, "pickle") {
		t.Errorf("analysis prompt missing votes/transcript")
	}
	f = &fakeLLM{json: []string{"nope", "still nope"}}
	if _, calls, err := Analyze(context.Background(), f, "m", seats, nil, ""); err == nil || len(calls) != 2 {
		t.Fatal("two invalid outputs must error (caller falls back to no tactics)")
	}
	// Invariant 5: bot_human games are excluded from analysis.
	seats[3].Kind = "bot_human"
	if _, _, err := Analyze(context.Background(), &fakeLLM{json: []string{analysis}}, "m", seats, nil, ""); err == nil {
		t.Fatal("bot_human game must be refused")
	}
	// Director: instruction for the human seat is dropped.
	f = &fakeLLM{json: []string{`{"seat_instructions":{"2":"say pickle once","4":"x"},"observed_tactics":[]}`}}
	ins, _, _, err := DirectRound(context.Background(), f, "m", []SeatInfo{{2, "Amber Owl", "ai"}, {3, "Green Newt", "ai"}, {5, "Grey Moth", "ai"}}, 2, "t")
	if err != nil || len(ins) != 1 || ins[2] != "say pickle once" {
		t.Fatalf("director: %v %v", ins, err)
	}
	if strings.Contains(f.calls[0].Messages[0].Content, "human") && strings.Contains(f.calls[0].Messages[0].Content, "Red Crow") {
		t.Error("director prompt must not name human seats")
	}
}
