package game

import (
	"encoding/json"
	"errors"
	"math/rand/v2"
	"reflect"
	"strings"
	"testing"
	"time"

	"hackathon2026/server/internal/ws"
)

var t0 = time.Date(2026, 11, 21, 12, 0, 0, 0, time.UTC)

const (
	userA = "11111111-aaaa-4aaa-8aaa-111111111111"
	userB = "22222222-bbbb-4bbb-8bbb-222222222222"
)

func testSpecs() []SeatSpec {
	return []SeatSpec{
		{Kind: KindHuman, UserID: userA},
		{Kind: KindHuman, UserID: userB},
		{Kind: KindAI, Provider: "stub", ModelID: "secret-model-x"},
		{Kind: KindAI, Provider: "stub", ModelID: "secret-model-y"},
		{Kind: KindAI, Provider: "stub", ModelID: "secret-model-z"},
	}
}

func newGame(seed uint64, specs []SeatSpec) *Game {
	cfg := DefaultConfig()
	return New("game-1", cfg, specs, rand.New(rand.NewPCG(seed, seed+1)))
}

func seatsOf(g *Game, k SeatKind) []int {
	var out []int
	for _, s := range g.Seats() {
		if s.Kind == k {
			out = append(out, s.Index)
		}
	}
	return out
}

// recorder keeps every effect and the time it was produced.
type recorder struct {
	effects []Effect
}

func (r *recorder) add(eff []Effect) []Effect { r.effects = append(r.effects, eff...); return eff }

func (r *recorder) messages() []Effect {
	var out []Effect
	for _, e := range r.effects {
		switch e.(type) {
		case Broadcast, SendTo:
			out = append(out, e)
		}
	}
	return out
}

func mustSubmit(t *testing.T, g *Game, now time.Time, s Submission) []Effect {
	t.Helper()
	eff, err := g.Submit(now, s)
	if err != nil {
		t.Fatalf("submit %+v in %s/%d: %v", s, g.Phase(), g.Round(), err)
	}
	return eff
}

// playAll submits for every seat in the current phase (seat order given), skipping when skip(seat) is true.
func playAll(t *testing.T, g *Game, now time.Time, order []int, skip func(seat int) bool, rec *recorder) {
	t.Helper()
	p, r := g.Phase(), g.Round()
	for _, seat := range order {
		if g.Phase() != p || g.Round() != r {
			return
		}
		switch p {
		case Intro, Ask:
			rec.add(mustSubmit(t, g, now, Submission{Seat: seat, Phase: p, Round: r, Kind: phaseMsgKind(p),
				Text: "hello from the table " + g.Seats()[seat].Alias, Skip: skip(seat)}))
		case Answer:
			for _, q := range g.questions {
				if q.Seat == seat || g.Phase() != p {
					continue
				}
				rec.add(mustSubmit(t, g, now, Submission{Seat: seat, Phase: p, Round: r, Kind: MsgAnswer,
					QuestionID: q.ID, Text: "an answer", Skip: skip(seat)}))
			}
		case Vote:
			target := (seat + 1) % len(g.seats)
			eff, err := g.CastVote(now, seat, target)
			if err != nil {
				t.Fatal(err)
			}
			rec.add(eff)
		}
	}
}

// runGame plays a full game where everyone submits and returns all effects.
func runGame(t *testing.T, g *Game, order []int) *recorder {
	t.Helper()
	rec := &recorder{}
	now := t0
	rec.add(g.Start(now))
	for i := 0; g.Phase() != Result; i++ {
		if i > 50 {
			t.Fatal("game did not finish")
		}
		now = now.Add(time.Second)
		if g.Phase() == Reveal {
			now = g.Deadline()
			rec.add(g.Tick(now))
			continue
		}
		playAll(t, g, now, order, func(int) bool { return false }, rec)
	}
	return rec
}

func TestPhaseSequenceByDeadline(t *testing.T) {
	g := newGame(1, testSpecs())
	g.cfg.Rounds = 2
	g.Start(t0)
	want := []struct {
		p Phase
		r int
	}{{Intro, 0}, {Ask, 1}, {Answer, 1}, {Reveal, 1}, {Ask, 2}, {Answer, 2}, {Reveal, 2}, {Vote, 2}, {Result, 2}}
	now := t0
	for i, w := range want {
		if g.Phase() != w.p || g.Round() != w.r {
			t.Fatalf("step %d: got %s/%d, want %s/%d", i, g.Phase(), g.Round(), w.p, w.r)
		}
		if w.p == Result {
			break
		}
		// A tick before the deadline does nothing.
		if eff := g.Tick(g.Deadline().Add(-time.Millisecond)); len(eff) != 0 {
			t.Fatalf("early tick produced effects: %v", eff)
		}
		if w.p == Ask { // one question, so ANSWER has something to answer
			mustSubmit(t, g, now, Submission{Seat: 0, Phase: Ask, Round: w.r, Kind: MsgQuestion, Text: "why?"})
		}
		now = g.Deadline()
		g.Tick(now)
	}
	if g.result == nil {
		t.Fatal("no result")
	}
}

func TestPhaseEndsEarlyWhenAllSubmitted(t *testing.T) {
	g := newGame(2, testSpecs())
	g.Start(t0)
	rec := &recorder{}
	now := t0.Add(3 * time.Second)
	order := []int{0, 1, 2, 3, 4}
	playAll(t, g, now, order[:4], func(int) bool { return false }, rec)
	if g.Phase() != Intro {
		t.Fatal("phase ended before all seats submitted")
	}
	playAll(t, g, now, order[4:], func(int) bool { return false }, rec)
	if g.Phase() != Ask {
		t.Fatalf("phase did not end early: %s", g.Phase())
	}
	if !g.Deadline().Equal(now.Add(g.cfg.TAsk)) {
		t.Fatal("next phase deadline not based on the early end time")
	}
}

func TestAnswerWithNoQuestionsEndsImmediately(t *testing.T) {
	g := newGame(3, testSpecs())
	g.Start(t0)
	g.Tick(g.Deadline()) // INTRO → ASK
	g.Tick(g.Deadline()) // ASK with no questions → ANSWER → complete at once → REVEAL
	if g.Phase() != Reveal || g.Round() != 1 {
		t.Fatalf("got %s/%d, want REVEAL/1", g.Phase(), g.Round())
	}
}

// walk calls fn for every key/value in a decoded JSON tree.
func walk(v any, fn func(key string, val any)) {
	switch x := v.(type) {
	case map[string]any:
		for k, vv := range x {
			fn(k, vv)
			walk(vv, fn)
		}
	case []any:
		for _, vv := range x {
			walk(vv, fn)
		}
	}
}

// Invariants 1 and 3: before game.result nothing reveals seat kind, model, provider, user id
// or username; message "kind" values are only message kinds.
func TestNoSecretsBeforeResult(t *testing.T) {
	specs := append(testSpecs()[:4], SeatSpec{Kind: KindBotHuman})
	specs[3] = SeatSpec{Kind: KindAI, Provider: "stub", ModelID: "secret-model-y"}
	for _, sp := range [][]SeatSpec{testSpecs(), specs} {
		g := newGame(4, sp)
		rec := runGame(t, g, []int{4, 3, 2, 1, 0})
		// snapshots in every phase are messages too
		forbidden := []string{userA, userB, "secret-model", "stub", "bot_human", "alice", "bob"}
		sawResult := false
		for _, e := range rec.messages() {
			var typ string
			var payload any
			switch m := e.(type) {
			case Broadcast:
				typ, payload = m.Type, m.Payload
			case SendTo:
				typ, payload = m.Type, m.Payload
			}
			if typ == ws.TGameResult {
				sawResult = true
				continue
			}
			if sawResult {
				t.Fatalf("message %s after game.result", typ)
			}
			checkPublic(t, typ, payload, forbidden)
		}
		if !sawResult {
			t.Fatal("no game.result")
		}
	}
}

func checkPublic(t *testing.T, typ string, payload any, forbidden []string) {
	t.Helper()
	b, err := json.Marshal(payload)
	if err != nil {
		t.Fatal(err)
	}
	s := string(b)
	for _, f := range forbidden {
		if strings.Contains(s, f) {
			t.Errorf("%s leaks %q: %s", typ, f, s)
		}
	}
	var tree any
	_ = json.Unmarshal(b, &tree)
	walk(tree, func(k string, v any) {
		switch k {
		case "model_id", "user_id", "provider", "persona", "username", "submitted_at", "typing":
			t.Errorf("%s has forbidden key %q", typ, k)
		case "kind":
			if v != "intro" && v != "question" && v != "answer" {
				t.Errorf("%s has kind=%v (seat kind leak)", typ, v)
			}
		}
	})
}

func TestSnapshotsHaveNoSecrets(t *testing.T) {
	g := newGame(5, testSpecs())
	g.Start(t0)
	now := t0
	for g.Phase() != Result {
		for seat := range g.Seats() {
			checkPublic(t, ws.TGameSnapshot, g.Snapshot(seat, now), []string{userA, userB, "secret-model", "stub"})
		}
		// half the seats submit, so snapshots are taken with pending sealed submissions
		if p := g.Phase(); p == Intro || p == Ask {
			for _, seat := range []int{0, 2} {
				mustSubmit(t, g, now, Submission{Seat: seat, Phase: p, Round: g.Round(), Kind: phaseMsgKind(p), Text: "q?"})
			}
			for seat := range g.Seats() {
				snap := g.Snapshot(seat, now)
				own := seat == 0 || seat == 2
				if (len(snap.Mine) == 1) != own {
					t.Fatalf("seat %d snapshot mine=%v", seat, snap.Mine)
				}
			}
		}
		now = g.Deadline()
		g.Tick(now)
	}
}

// Invariant 2: a submission that does not finish the phase produces no message to anyone but
// the submitter, and nothing that names the submitter goes to the table.
func TestSubmitIsSealed(t *testing.T) {
	g := newGame(6, testSpecs())
	g.Start(t0)
	for _, seat := range []int{3, 0, 1, 4} {
		eff := mustSubmit(t, g, t0.Add(time.Second), Submission{Seat: seat, Phase: Intro, Kind: MsgIntro, Text: "hi"})
		for _, e := range eff {
			switch m := e.(type) {
			case Broadcast:
				t.Fatalf("submit by seat %d broadcast %s", seat, m.Type)
			case SendTo:
				if m.Seat != seat || m.Type != ws.TSubmitAck {
					t.Fatalf("submit by seat %d sent %s to seat %d", seat, m.Type, m.Seat)
				}
			case Persist:
			default:
				t.Fatalf("unexpected effect %T", e)
			}
		}
	}
	// The last submission ends the phase: exactly one reveal for INTRO.
	eff := mustSubmit(t, g, t0.Add(2*time.Second), Submission{Seat: 2, Phase: Intro, Kind: MsgIntro, Text: "yo"})
	reveals := 0
	for _, e := range eff {
		if b, ok := e.(Broadcast); ok && b.Type == ws.TPhaseReveal {
			reveals++
		}
	}
	if reveals != 1 {
		t.Fatalf("want 1 reveal, got %d", reveals)
	}
}

// The reveal must not depend on who submitted first (AI seats answer instantly).
func TestRevealIndependentOfSubmitOrder(t *testing.T) {
	a := runGame(t, newGame(7, testSpecs()), []int{0, 1, 2, 3, 4})
	b := runGame(t, newGame(7, testSpecs()), []int{4, 2, 0, 3, 1})
	reveal := func(r *recorder) []string {
		var out []string
		for _, e := range r.messages() {
			if m, ok := e.(Broadcast); ok && m.Type == ws.TPhaseReveal {
				rv := m.Payload.(ws.PhaseReveal)
				for i := range rv.Items {
					rv.Items[i].ID = "" // ids are random per submission
					rv.Items[i].QuestionID = ""
				}
				j, _ := json.Marshal(rv)
				out = append(out, string(j))
			}
		}
		return out
	}
	ra, rb := reveal(a), reveal(b)
	if !reflect.DeepEqual(ra, rb) {
		t.Fatalf("reveals differ by submit order:\n%v\n%v", ra, rb)
	}
	for _, r := range ra {
		var rv ws.PhaseReveal
		_ = json.Unmarshal([]byte(r), &rv)
		for i := 1; i < len(rv.Items); i++ {
			if rv.Phase != string(Answer) && rv.Items[i].Seat < rv.Items[i-1].Seat {
				t.Fatalf("reveal not in seat order: %s", r)
			}
		}
	}
}

// A skip and a missing submission look identical to players, but are stored differently.
func TestSkipAndMissingLookTheSame(t *testing.T) {
	g := newGame(8, testSpecs())
	rec := &recorder{}
	rec.add(g.Start(t0))
	rec.add(mustSubmit(t, g, t0.Add(time.Second), Submission{Seat: 0, Phase: Intro, Kind: MsgIntro, Skip: true}))
	rec.add(g.Tick(g.Deadline()))
	var rv ws.PhaseReveal
	var recs []MessageRecord
	for _, e := range rec.effects {
		switch m := e.(type) {
		case Broadcast:
			if m.Type == ws.TPhaseReveal {
				rv = m.Payload.(ws.PhaseReveal)
			}
		case Persist:
			if r, ok := m.Record.(MessageRecord); ok {
				recs = append(recs, r)
			}
		}
	}
	if len(rv.Items) != 5 {
		t.Fatalf("want 5 intro items, got %d", len(rv.Items))
	}
	for _, it := range rv.Items {
		if it.Text != nil {
			t.Fatalf("seat %d text should be null", it.Seat)
		}
	}
	if len(recs) != 5 {
		t.Fatalf("want 5 message records, got %d", len(recs))
	}
	for _, r := range recs {
		if !r.Skipped {
			t.Fatal("record not skipped")
		}
		if (r.SubmittedAt != nil) != (r.Seat == 0) {
			t.Fatalf("seat %d submitted_at=%v", r.Seat, r.SubmittedAt)
		}
	}
}

// Invariant 4: every submission, skip, missing slot, vote and note is persisted with a server time.
func TestEverythingPersisted(t *testing.T) {
	g := newGame(9, testSpecs())
	rec := runGame(t, g, []int{0, 1, 2, 3, 4})
	eff, err := g.SetNote(t0, 0, 1, "?", "x") // RESULT: rejected
	if err == nil || eff != nil {
		t.Fatal("note accepted after the game")
	}
	var msgs, votes, games, finishes int
	for _, e := range rec.effects {
		p, ok := e.(Persist)
		if !ok {
			continue
		}
		switch r := p.Record.(type) {
		case MessageRecord:
			msgs++
			if r.SubmittedAt == nil || r.SubmittedAt.IsZero() || r.PhaseStartedAt.IsZero() {
				t.Fatalf("message without timestamps: %+v", r)
			}
		case VoteRecord:
			votes++
			if r.CastAt.IsZero() {
				t.Fatal("vote without timestamp")
			}
		case GameRecord:
			games++
		case FinishRecord:
			finishes++
		}
	}
	// 5 intros + per round (5 questions + 5×4 answers) × 2 rounds = 55
	if msgs != 55 || votes != 5 || games != 1 || finishes != 1 {
		t.Fatalf("msgs=%d votes=%d games=%d finishes=%d", msgs, votes, games, finishes)
	}
}

func TestNotesPersistedAndPrivate(t *testing.T) {
	g := newGame(10, testSpecs())
	g.Start(t0)
	eff, err := g.SetNote(t0.Add(time.Second), 0, 3, "ai", "too fast")
	if err != nil {
		t.Fatal(err)
	}
	var nr *NoteRecord
	for _, e := range eff {
		switch m := e.(type) {
		case Persist:
			r := m.Record.(NoteRecord)
			nr = &r
		case SendTo:
			if m.Seat != 0 {
				t.Fatal("note ack sent to another seat")
			}
		case Broadcast:
			t.Fatal("note broadcast")
		}
	}
	if nr == nil || nr.Phase != Intro || nr.TS.IsZero() || nr.Mark != "ai" {
		t.Fatalf("bad note record %+v", nr)
	}
	if len(g.Snapshot(0, t0).Notes) != 1 || len(g.Snapshot(1, t0).Notes) != 0 {
		t.Fatal("notes not private to the owner")
	}
	for _, bad := range []struct {
		owner, target int
		mark, note    string
	}{{0, 0, "ai", ""}, {0, 9, "ai", ""}, {0, 1, "robot", ""}, {0, 1, "?", strings.Repeat("x", 61)}} {
		if _, err := g.SetNote(t0, bad.owner, bad.target, bad.mark, bad.note); !errors.Is(err, ErrRejected) {
			t.Fatalf("note %+v accepted", bad)
		}
	}
}

func TestSubmitValidation(t *testing.T) {
	g := newGame(11, testSpecs())
	g.Start(t0)
	ok := Submission{Seat: 0, Phase: Intro, Kind: MsgIntro, Text: "hi"}
	cases := []struct {
		name string
		mod  func(s *Submission)
	}{
		{"wrong phase", func(s *Submission) { s.Phase = Ask }},
		{"wrong round", func(s *Submission) { s.Round = 1 }},
		{"wrong kind", func(s *Submission) { s.Kind = MsgAnswer }},
		{"bad seat", func(s *Submission) { s.Seat = 7 }},
		{"empty", func(s *Submission) { s.Text = "   " }},
		{"too long", func(s *Submission) { s.Text = strings.Repeat("é", MaxIntro+1) }},
	}
	for _, c := range cases {
		s := ok
		c.mod(&s)
		if _, err := g.Submit(t0, s); !errors.Is(err, ErrRejected) {
			t.Errorf("%s: accepted (err=%v)", c.name, err)
		}
	}
	if _, err := g.Submit(t0, Submission{Seat: 0, Phase: Intro, Kind: MsgIntro, Text: strings.Repeat("é", MaxIntro)}); err != nil {
		t.Fatalf("160 runes rejected: %v", err)
	}
	if _, err := g.Submit(t0, ok); !errors.Is(err, ErrRejected) {
		t.Fatal("second submission accepted")
	}
	// Answering your own question is rejected.
	g.Tick(g.Deadline())
	mustSubmit(t, g, t0, Submission{Seat: 1, Phase: Ask, Round: 1, Kind: MsgQuestion, Text: "why?"})
	g.Tick(g.Deadline())
	if g.Phase() != Answer || len(g.questions) != 1 {
		t.Fatalf("got %s with %d questions", g.Phase(), len(g.questions))
	}
	q := g.questions[0]
	if _, err := g.Submit(t0, Submission{Seat: 1, Phase: Answer, Round: 1, Kind: MsgAnswer, QuestionID: q.ID, Text: "me"}); !errors.Is(err, ErrRejected) {
		t.Fatal("answered own question")
	}
	if _, err := g.Submit(t0, Submission{Seat: 2, Phase: Answer, Round: 1, Kind: MsgAnswer, QuestionID: "nope", Text: "x"}); !errors.Is(err, ErrRejected) {
		t.Fatal("answered unknown question")
	}
}

func TestTelemetryOnlyForHumans(t *testing.T) {
	g := newGame(12, testSpecs())
	g.Start(t0)
	ms, ec := 1200, 3
	for _, s := range g.Seats() {
		eff := mustSubmit(t, g, t0, Submission{Seat: s.Index, Phase: Intro, Kind: MsgIntro, Text: "hi", FirstKeystrokeMs: &ms, EditCount: &ec})
		r := eff[0].(Persist).Record.(MessageRecord)
		if (r.FirstKeystrokeMs != nil) != (s.Kind == KindHuman) {
			t.Fatalf("seat kind %s telemetry=%v", s.Kind, r.FirstKeystrokeMs)
		}
	}
}

func TestRunAgentOnlyForNonHumans(t *testing.T) {
	sp := testSpecs()
	sp[1] = SeatSpec{Kind: KindBotHuman}
	g := newGame(13, sp)
	rec := runGame(t, g, []int{0, 1, 2, 3, 4})
	perPhase := map[Phase]int{}
	for _, e := range rec.effects {
		if ra, ok := e.(RunAgent); ok {
			if g.Seats()[ra.Seat].Kind == KindHuman {
				t.Fatal("agent run for a human seat")
			}
			perPhase[ra.Phase]++
			if ra.Phase == Vote {
				for _, c := range ra.Candidates {
					if c == ra.Seat {
						t.Fatal("agent may vote for itself")
					}
				}
			}
			for _, q := range ra.Questions {
				if q.Seat == ra.Seat {
					t.Fatal("agent asked to answer its own question")
				}
			}
		}
	}
	want := map[Phase]int{Intro: 4, Ask: 8, Answer: 8, Vote: 4}
	if !reflect.DeepEqual(perPhase, want) {
		t.Fatalf("agent runs %v, want %v", perPhase, want)
	}
}

func TestScoring(t *testing.T) {
	// Seats after New are shuffled, so the test builds the game and maps roles to seats.
	type votes map[string]string // role → role
	cases := []struct {
		name    string
		bot     bool
		v       votes
		pointsA int
		pointsB int
		stolen  map[string]int
	}{
		{"mutual", false, votes{"A": "B", "B": "A", "X": "A"}, 3, 3, map[string]int{"X": 0, "Y": 0, "Z": 0}},
		{"one-sided", false, votes{"A": "B", "B": "X"}, 1, 1, map[string]int{"X": 1}},
		{"miss", false, votes{"A": "X", "B": "Y", "Y": "A"}, 0, 0, map[string]int{"X": 1, "Y": 1, "Z": 0}},
		{"no votes", false, votes{}, 0, 0, map[string]int{"X": 0}},
		{"bot human counts as human", true, votes{"A": "B", "B": "A"}, 3, 3, map[string]int{"X": 0}},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			sp := testSpecs()
			if c.bot {
				sp[1] = SeatSpec{Kind: KindBotHuman}
			}
			g := newGame(14, sp)
			role := map[string]int{}
			ai := []string{"X", "Y", "Z"}
			for _, s := range g.Seats() {
				switch {
				case s.UserID == userA:
					role["A"] = s.Index
				case s.UserID == userB || s.Kind == KindBotHuman:
					role["B"] = s.Index
				default:
					role[ai[0]] = s.Index
					ai = ai[1:]
				}
			}
			g.Start(t0)
			for g.Phase() != Vote {
				g.Tick(g.Deadline())
			}
			for from, to := range c.v {
				if _, err := g.CastVote(t0, role[from], role[to]); err != nil {
					t.Fatal(err)
				}
			}
			g.Tick(g.Deadline())
			if g.Phase() != Result {
				t.Fatal("not finished")
			}
			res := map[int]ws.SeatResult{}
			for _, r := range g.result.Results {
				res[r.Seat] = r
			}
			if got := res[role["A"]].Points; got != c.pointsA {
				t.Errorf("A points %d, want %d", got, c.pointsA)
			}
			if got := res[role["B"]].Points; got != c.pointsB {
				t.Errorf("B points %d, want %d", got, c.pointsB)
			}
			for r, n := range c.stolen {
				if vs := res[role[r]].VotesStolen; vs == nil || *vs != n {
					t.Errorf("%s votes stolen %v, want %d", r, vs, n)
				}
			}
			if res[role["A"]].VotesStolen != nil {
				t.Error("human seat has votes_stolen")
			}
			kinds := map[string]int{}
			for _, s := range g.result.Seats {
				kinds[s.Kind]++
			}
			if c.bot && kinds["bot_human"] != 1 {
				t.Errorf("result seats %v", kinds)
			}
		})
	}
}

func TestVoteValidation(t *testing.T) {
	g := newGame(15, testSpecs())
	g.Start(t0)
	if _, err := g.CastVote(t0, 0, 1); !errors.Is(err, ErrRejected) {
		t.Fatal("vote accepted outside VOTE")
	}
	for g.Phase() != Vote {
		g.Tick(g.Deadline())
	}
	if _, err := g.CastVote(t0, 0, 0); !errors.Is(err, ErrRejected) {
		t.Fatal("self vote accepted")
	}
	if _, err := g.CastVote(t0, 0, 1); err != nil {
		t.Fatal(err)
	}
	if _, err := g.CastVote(t0, 0, 2); !errors.Is(err, ErrRejected) {
		t.Fatal("vote changed")
	}
}

func TestAliasesAndShuffle(t *testing.T) {
	seen := map[string]bool{}
	firstHuman := map[int]bool{}
	for seed := uint64(0); seed < 30; seed++ {
		g := newGame(seed, testSpecs())
		names := map[string]bool{}
		for _, s := range g.Seats() {
			if names[s.Alias] {
				t.Fatalf("duplicate alias %s", s.Alias)
			}
			names[s.Alias] = true
			seen[s.Alias] = true
			if s.UserID == userA {
				firstHuman[s.Index] = true
			}
		}
	}
	if len(firstHuman) < 3 {
		t.Fatalf("seat order not shuffled: human A only at %v", firstHuman)
	}
	if len(seen) < 20 {
		t.Fatalf("aliases not random: %d distinct", len(seen))
	}
}

func TestConfigFromEnv(t *testing.T) {
	env := func(m map[string]string) func(string) string { return func(k string) string { return m[k] } }
	c, err := ConfigFromEnv(env(nil))
	if err != nil || c != DefaultConfig() {
		t.Fatalf("defaults: %v %+v", err, c)
	}
	c, err = ConfigFromEnv(env(map[string]string{"T_INTRO": "15", "T_VOTE": "20", "ROUNDS": "1", "AI_PER_GAME": "2"}))
	if err != nil || c.TIntro != 15*time.Second || c.TVote != 20*time.Second || c.Rounds != 1 || c.AIPerGame != 2 {
		t.Fatalf("parsed: %v %+v", err, c)
	}
	for _, bad := range []map[string]string{{"T_ASK": "3"}, {"ROUNDS": "4"}, {"HUMANS_PER_GAME": "1"}, {"T_REVEAL": "abc"}} {
		if _, err := ConfigFromEnv(env(bad)); err == nil {
			t.Errorf("%v accepted", bad)
		}
	}
}
