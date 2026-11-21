// Package game is the pure state machine for one table (SPEC §2). It does no I/O: every method
// takes the current time, mutates the game and returns effects for the hub to execute
// (send, persist, set a timer, run an agent). Randomness comes from an injected *rand.Rand.
//
// Phases: LOBBY → INTRO → (ASK → ANSWER → REVEAL) × Rounds → VOTE → RESULT.
// Submissions are sealed: a submit only produces an ack to the submitter and a persist record.
// Everything submitted in a phase is revealed in one phase.reveal when the phase ends, at the
// deadline or as soon as every seat has submitted or skipped every slot.
package game

import (
	"errors"
	"fmt"
	"math/rand/v2"
	"sort"
	"strings"
	"time"
	"unicode/utf8"

	"hackathon2026/server/internal/ws"
)

type Phase string

const (
	Lobby  Phase = "LOBBY"
	Intro  Phase = "INTRO"
	Ask    Phase = "ASK"
	Answer Phase = "ANSWER"
	Reveal Phase = "REVEAL"
	Vote   Phase = "VOTE"
	Result Phase = "RESULT"
)

// SeatKind is secret until RESULT (invariant 1).
type SeatKind string

const (
	KindHuman    SeatKind = "human"
	KindAI       SeatKind = "ai"
	KindBotHuman SeatKind = "bot_human" // dev/demo filler that plays the human role; excluded from stats
)

// PlaysHuman is true for seats that count as human for scoring (humans and their bot stand-ins).
func (k SeatKind) PlaysHuman() bool { return k == KindHuman || k == KindBotHuman }

// MsgKind is the kind of a table message.
type MsgKind string

const (
	MsgIntro    MsgKind = "intro"
	MsgQuestion MsgKind = "question"
	MsgAnswer   MsgKind = "answer"
)

func phaseMsgKind(p Phase) MsgKind {
	switch p {
	case Intro:
		return MsgIntro
	case Ask:
		return MsgQuestion
	case Answer:
		return MsgAnswer
	}
	return ""
}

func maxLen(k MsgKind) int {
	switch k {
	case MsgIntro:
		return MaxIntro
	case MsgQuestion:
		return MaxAsk
	}
	return MaxAnswer
}

// SeatSpec is who sits in a seat. Only the hub and the store see it.
type SeatSpec struct {
	Kind     SeatKind
	UserID   string // humans only
	Provider string // ai only
	ModelID  string // ai only
}

type Seat struct {
	Index      int
	Alias      string
	SpriteSeed int
	SeatSpec
}

// ---- effects ----

type Effect interface{ effect() }

// Broadcast goes to every seat (the hub delivers it to the connected humans).
type Broadcast struct {
	Type    string
	Payload any
}

// SendTo goes to one seat only.
type SendTo struct {
	Seat    int
	Type    string
	Payload any
}

// SetTimer asks the hub to call Tick at (or after) At. A newer SetTimer replaces the old one.
type SetTimer struct{ At time.Time }

// RunAgent asks the hub to run the agent for a non-human seat. The agent's reply comes back
// through Submit/Vote like a human's; failures become skips.
type RunAgent struct {
	Seat       int
	Phase      Phase
	Round      int
	Deadline   time.Time
	Questions  []ws.Question // ANSWER: the questions this seat should answer
	Candidates []int         // VOTE: seats this seat may vote for
}

// Persist is a record for the store.
type Persist struct{ Record any }

// Finished is emitted once, when the game reaches RESULT.
type Finished struct{}

func (Broadcast) effect() {}
func (SendTo) effect()    {}
func (SetTimer) effect()  {}
func (RunAgent) effect()  {}
func (Persist) effect()   {}
func (Finished) effect()  {}

// ---- persist records (server timestamps, invariant 4) ----

type GameRecord struct {
	ID        string
	Config    Config
	StartedAt time.Time
	Seats     []Seat
	// Set by the hub, which knows what plays the AI seats.
	Difficulty    string
	PromptVersion string
}

type MessageRecord struct {
	ID               string
	GameID           string
	Round            int
	Phase            Phase
	Kind             MsgKind
	Seat             int
	QuestionID       string
	Text             string
	Skipped          bool
	PhaseStartedAt   time.Time
	SubmittedAt      *time.Time // nil when the seat sent nothing before the deadline
	FirstKeystrokeMs *int
	EditCount        *int
}

type VoteRecord struct {
	GameID string
	Voter  int
	Target int
	CastAt time.Time
}

type NoteRecord struct {
	GameID string
	Owner  int
	Target int
	Mark   string
	Note   string
	Round  int
	Phase  Phase
	TS     time.Time
}

type FinishRecord struct {
	GameID  string
	EndedAt time.Time
	Results []ws.SeatResult
}

// ---- events from players and agents ----

type Submission struct {
	Seat             int
	Phase            Phase
	Round            int
	Kind             MsgKind
	QuestionID       string
	Text             string
	Skip             bool
	FirstKeystrokeMs *int
	EditCount        *int
}

// ErrRejected wraps every validation error, so the hub can tell a bad request from a bug.
var ErrRejected = errors.New("rejected")

func reject(format string, a ...any) error {
	return fmt.Errorf("%w: %s", ErrRejected, fmt.Sprintf(format, a...))
}

// ---- state ----

type slot struct {
	seat int
	qid  string
}

type sub struct {
	id      string
	text    string
	skipped bool
}

type Game struct {
	ID    string
	cfg   Config
	rng   *rand.Rand
	seats []Seat

	phase        Phase
	round        int
	phaseStarted time.Time
	deadline     time.Time

	questions []ws.Question // current round's questions (ANSWER, REVEAL)
	subs      map[slot]sub  // current phase only
	reveals   []ws.PhaseReveal

	votes  map[int]int
	notes  map[[2]int]ws.Note
	result *ws.GameResult
}

// New seats the given players in a shuffled order with random aliases and sprite seeds.
func New(id string, cfg Config, specs []SeatSpec, rng *rand.Rand) *Game {
	specs = append([]SeatSpec(nil), specs...)
	rng.Shuffle(len(specs), func(i, j int) { specs[i], specs[j] = specs[j], specs[i] })
	aliases := pickAliases(rng, len(specs))
	seats := make([]Seat, len(specs))
	for i, s := range specs {
		seats[i] = Seat{Index: i, Alias: aliases[i], SpriteSeed: rng.IntN(1 << 20), SeatSpec: s}
	}
	return &Game{
		ID: id, cfg: cfg, rng: rng, seats: seats, phase: Lobby,
		subs: map[slot]sub{}, votes: map[int]int{}, notes: map[[2]int]ws.Note{},
	}
}

func (g *Game) Phase() Phase              { return g.phase }
func (g *Game) Round() int                { return g.round }
func (g *Game) Deadline() time.Time       { return g.deadline }
func (g *Game) Seats() []Seat             { return append([]Seat(nil), g.seats...) }
func (g *Game) Config() Config            { return g.cfg }
func (g *Game) Reveals() []ws.PhaseReveal { return append([]ws.PhaseReveal(nil), g.reveals...) }

func (g *Game) publicSeats() []ws.PublicSeat {
	out := make([]ws.PublicSeat, len(g.seats))
	for i, s := range g.seats {
		out[i] = ws.PublicSeat{Seat: s.Index, Alias: s.Alias, SpriteSeed: s.SpriteSeed}
	}
	return out
}

func (g *Game) publicConfig() ws.PublicConfig {
	return ws.PublicConfig{Seats: len(g.seats), Rounds: g.cfg.Rounds, MaxIntro: MaxIntro, MaxAsk: MaxAsk, MaxAnswer: MaxAnswer}
}

// Start moves LOBBY → INTRO.
func (g *Game) Start(now time.Time) []Effect {
	if g.phase != Lobby {
		return nil
	}
	eff := []Effect{Persist{GameRecord{ID: g.ID, Config: g.cfg, StartedAt: now, Seats: g.Seats()}}}
	for _, s := range g.seats {
		eff = append(eff, SendTo{Seat: s.Index, Type: ws.TGameStart, Payload: ws.GameStart{
			GameID: g.ID, MySeat: s.Index, Seats: g.publicSeats(), Config: g.publicConfig(),
		}})
	}
	eff = append(eff, g.enter(now, Intro, 0)...)
	return g.settle(now, eff)
}

// Tick ends the phase if its deadline has passed. Early or stale ticks do nothing.
func (g *Game) Tick(now time.Time) []Effect {
	if g.phase == Lobby || g.phase == Result || now.Before(g.deadline) {
		return nil
	}
	return g.settle(now, g.finishPhase(now))
}

// Submit records one sealed submission (text or skip) for a slot.
func (g *Game) Submit(now time.Time, s Submission) ([]Effect, error) {
	if s.Seat < 0 || s.Seat >= len(g.seats) {
		return nil, reject("no such seat")
	}
	want := phaseMsgKind(g.phase)
	if want == "" || s.Phase != g.phase || s.Round != g.round {
		return nil, reject("phase is over")
	}
	if s.Kind != want {
		return nil, reject("this phase takes %s messages", want)
	}
	if want != MsgAnswer {
		s.QuestionID = ""
	}
	sl := slot{seat: s.Seat, qid: s.QuestionID}
	if !g.expects(sl) {
		return nil, reject("nothing to answer there")
	}
	if _, done := g.subs[sl]; done {
		return nil, reject("already sent")
	}
	text := ""
	if !s.Skip {
		text = clean(s.Text)
		if text == "" {
			return nil, reject("empty message: send a skip instead")
		}
		if n := utf8.RuneCountInString(text); n > maxLen(want) {
			return nil, reject("too long: %d of %d characters", n, maxLen(want))
		}
	}
	if g.seats[s.Seat].Kind != KindHuman { // client telemetry is for humans only
		s.FirstKeystrokeMs, s.EditCount = nil, nil
	}
	id := g.newID()
	g.subs[sl] = sub{id: id, text: text, skipped: s.Skip}
	at := now
	eff := []Effect{
		Persist{MessageRecord{
			ID: id, GameID: g.ID, Round: g.round, Phase: g.phase, Kind: want, Seat: s.Seat,
			QuestionID: s.QuestionID, Text: text, Skipped: s.Skip, PhaseStartedAt: g.phaseStarted,
			SubmittedAt: &at, FirstKeystrokeMs: s.FirstKeystrokeMs, EditCount: s.EditCount,
		}},
		SendTo{Seat: s.Seat, Type: ws.TSubmitAck, Payload: ws.SubmitAck{
			Phase: string(g.phase), Round: g.round, Kind: string(want), QuestionID: s.QuestionID, Skipped: s.Skip,
		}},
	}
	return g.settle(now, eff), nil
}

// CastVote records a seat's vote. Votes are final once cast.
func (g *Game) CastVote(now time.Time, voter, target int) ([]Effect, error) {
	if g.phase != Vote {
		return nil, reject("voting is not open")
	}
	if voter < 0 || voter >= len(g.seats) || target < 0 || target >= len(g.seats) {
		return nil, reject("no such seat")
	}
	if voter == target {
		return nil, reject("you can't vote for yourself")
	}
	if _, done := g.votes[voter]; done {
		return nil, reject("already voted")
	}
	g.votes[voter] = target
	eff := []Effect{
		Persist{VoteRecord{GameID: g.ID, Voter: voter, Target: target, CastAt: now}},
		SendTo{Seat: voter, Type: ws.TVoteAck, Payload: ws.VoteAck{TargetSeat: target}},
	}
	return g.settle(now, eff), nil
}

var validMarks = map[string]bool{"none": true, "?": true, "!": true, "human": true, "ai": true}

// SetNote changes a private suspicion marker (SPEC §4). Every change is persisted.
func (g *Game) SetNote(now time.Time, owner, target int, mark, note string) ([]Effect, error) {
	if g.phase == Lobby || g.phase == Result {
		return nil, reject("the game is not running")
	}
	if owner < 0 || owner >= len(g.seats) || target < 0 || target >= len(g.seats) || owner == target {
		return nil, reject("no such seat")
	}
	if !validMarks[mark] {
		return nil, reject("unknown mark")
	}
	note = clean(note)
	if utf8.RuneCountInString(note) > MaxNote {
		return nil, reject("note too long")
	}
	n := ws.Note{TargetSeat: target, Mark: mark, Note: note}
	g.notes[[2]int{owner, target}] = n
	return []Effect{
		Persist{NoteRecord{GameID: g.ID, Owner: owner, Target: target, Mark: mark, Note: note, Round: g.round, Phase: g.phase, TS: now}},
		SendTo{Seat: owner, Type: ws.TNoteAck, Payload: n},
	}, nil
}

// Snapshot is everything seat may see right now (game.snapshot).
func (g *Game) Snapshot(seat int, now time.Time) ws.Snapshot {
	snap := ws.Snapshot{
		GameID: g.ID, MySeat: seat, Seats: g.publicSeats(), Config: g.publicConfig(),
		Phase: string(g.phase), Round: g.round, ServerTS: now.UnixMilli(),
		Reveals: g.Reveals(), Mine: []ws.SubmitAck{}, Notes: []ws.Note{}, Result: g.result,
	}
	if !g.deadline.IsZero() {
		snap.DeadlineTS = g.deadline.UnixMilli()
	}
	if g.phase == Answer || g.phase == Reveal {
		snap.Questions = append([]ws.Question(nil), g.questions...)
	}
	if snap.Reveals == nil {
		snap.Reveals = []ws.PhaseReveal{}
	}
	for _, sl := range g.expectedSlots() {
		if sl.seat != seat {
			continue
		}
		if s, ok := g.subs[sl]; ok {
			snap.Mine = append(snap.Mine, ws.SubmitAck{
				Phase: string(g.phase), Round: g.round, Kind: string(phaseMsgKind(g.phase)), QuestionID: sl.qid, Skipped: s.skipped,
			})
		}
	}
	if v, ok := g.votes[seat]; ok {
		snap.MyVote = &v
	}
	for t := range g.seats {
		if n, ok := g.notes[[2]int{seat, t}]; ok {
			snap.Notes = append(snap.Notes, n)
		}
	}
	return snap
}

// ---- phase machinery ----

// settle appends phase transitions for as long as the current phase is complete.
func (g *Game) settle(now time.Time, eff []Effect) []Effect {
	for g.phase != Result && g.phase != Lobby && g.complete() {
		eff = append(eff, g.finishPhase(now)...)
	}
	return eff
}

func (g *Game) enter(now time.Time, p Phase, round int) []Effect {
	g.phase, g.round = p, round
	g.phaseStarted = now
	g.deadline = now.Add(g.cfg.duration(p))
	g.subs = map[slot]sub{}
	if p == Ask {
		g.questions = nil
	}
	ps := ws.PhaseStart{Phase: string(p), Round: round, DeadlineTS: g.deadline.UnixMilli(), ServerTS: now.UnixMilli()}
	if p == Answer {
		ps.Questions = append([]ws.Question(nil), g.questions...)
	}
	eff := []Effect{Broadcast{Type: ws.TPhaseStart, Payload: ps}, SetTimer{At: g.deadline}}
	if p == Reveal {
		return eff
	}
	for _, s := range g.seats {
		if s.Kind == KindHuman {
			continue
		}
		ra := RunAgent{Seat: s.Index, Phase: p, Round: round, Deadline: g.deadline}
		switch p {
		case Answer:
			for _, q := range g.questions {
				if q.Seat != s.Index {
					ra.Questions = append(ra.Questions, q)
				}
			}
		case Vote:
			for _, o := range g.seats {
				if o.Index != s.Index {
					ra.Candidates = append(ra.Candidates, o.Index)
				}
			}
		}
		eff = append(eff, ra)
	}
	return eff
}

// expectedSlots lists the slots of the current phase in reveal order: by question, then seat.
func (g *Game) expectedSlots() []slot {
	var out []slot
	switch g.phase {
	case Intro, Ask:
		for _, s := range g.seats {
			out = append(out, slot{seat: s.Index})
		}
	case Answer:
		for _, q := range g.questions {
			for _, s := range g.seats {
				if s.Index != q.Seat {
					out = append(out, slot{seat: s.Index, qid: q.ID})
				}
			}
		}
	}
	return out
}

func (g *Game) expects(sl slot) bool {
	for _, e := range g.expectedSlots() {
		if e == sl {
			return true
		}
	}
	return false
}

func (g *Game) complete() bool {
	switch g.phase {
	case Intro, Ask, Answer:
		for _, sl := range g.expectedSlots() {
			if _, ok := g.subs[sl]; !ok {
				return false
			}
		}
		return true
	case Vote:
		return len(g.votes) == len(g.seats)
	}
	return false // REVEAL always runs its full time
}

// finishPhase reveals the phase's content (one message) and enters the next phase.
func (g *Game) finishPhase(now time.Time) []Effect {
	var eff []Effect
	switch g.phase {
	case Intro, Ask, Answer:
		kind := phaseMsgKind(g.phase)
		rev := ws.PhaseReveal{Phase: string(g.phase), Round: g.round, Items: []ws.RevealItem{}}
		var questions []ws.Question
		for _, sl := range g.expectedSlots() {
			s, ok := g.subs[sl]
			if !ok { // missing: persisted as a skip without a submit time; looks like a skip to players
				s = sub{id: g.newID(), skipped: true}
				eff = append(eff, Persist{MessageRecord{
					ID: s.id, GameID: g.ID, Round: g.round, Phase: g.phase, Kind: kind, Seat: sl.seat,
					QuestionID: sl.qid, Skipped: true, PhaseStartedAt: g.phaseStarted,
				}})
			}
			item := ws.RevealItem{ID: s.id, Seat: sl.seat, Kind: string(kind), QuestionID: sl.qid}
			if !s.skipped {
				t := s.text
				item.Text = &t
				if g.phase == Ask {
					questions = append(questions, ws.Question{ID: s.id, Seat: sl.seat, Text: t})
				}
			}
			rev.Items = append(rev.Items, item)
		}
		g.reveals = append(g.reveals, rev)
		eff = append(eff, Broadcast{Type: ws.TPhaseReveal, Payload: rev})
		switch g.phase {
		case Intro:
			eff = append(eff, g.enter(now, Ask, 1)...)
		case Ask:
			g.questions = questions
			eff = append(eff, g.enter(now, Answer, g.round)...)
		case Answer:
			eff = append(eff, g.enter(now, Reveal, g.round)...)
		}
	case Reveal:
		if g.round < g.cfg.Rounds {
			eff = append(eff, g.enter(now, Ask, g.round+1)...)
		} else {
			eff = append(eff, g.enter(now, Vote, g.round)...)
		}
	case Vote:
		g.phase = Result
		g.deadline = time.Time{}
		g.subs = map[slot]sub{}
		g.result = g.score()
		eff = append(eff,
			Persist{FinishRecord{GameID: g.ID, EndedAt: now, Results: g.result.Results}},
			Broadcast{Type: ws.TGameResult, Payload: *g.result},
			Finished{},
		)
	}
	return eff
}

// score applies SPEC §2.3 (co-op). Only votes cast by human-playing seats count.
func (g *Game) score() *ws.GameResult {
	res := &ws.GameResult{GameID: g.ID, Seats: []ws.ResultSeat{}, Votes: []ws.VoteArrow{}, Results: []ws.SeatResult{}}
	isHuman := func(i int) bool { return g.seats[i].Kind.PlaysHuman() }
	for _, s := range g.seats {
		res.Seats = append(res.Seats, ws.ResultSeat{Seat: s.Index, Alias: s.Alias, SpriteSeed: s.SpriteSeed, Kind: string(s.Kind), ModelID: s.ModelID})
	}
	voters := make([]int, 0, len(g.votes))
	for v := range g.votes {
		voters = append(voters, v)
	}
	sort.Ints(voters)
	for _, v := range voters {
		res.Votes = append(res.Votes, ws.VoteArrow{Voter: v, Target: g.votes[v]})
	}
	for _, s := range g.seats {
		r := ws.SeatResult{Seat: s.Index}
		if isHuman(s.Index) {
			t, voted := g.votes[s.Index]
			r.FoundOther = voted && isHuman(t)
			for o, ot := range g.votes {
				if o != s.Index && isHuman(o) && ot == s.Index {
					r.WasFound = true
				}
			}
			r.Mutual = r.FoundOther && g.votes[t] == s.Index && hasVote(g.votes, t)
			r.Points = b2i(r.FoundOther) + b2i(r.WasFound) + b2i(r.Mutual)
		} else {
			n := 0
			for o, ot := range g.votes {
				if isHuman(o) && ot == s.Index {
					n++
				}
			}
			r.VotesStolen = &n
		}
		res.Results = append(res.Results, r)
	}
	return res
}

func hasVote(v map[int]int, seat int) bool { _, ok := v[seat]; return ok }

func b2i(b bool) int {
	if b {
		return 1
	}
	return 0
}

// clean trims and flattens line breaks, so one message stays one line on the table.
func clean(s string) string {
	s = strings.Map(func(r rune) rune {
		if r == '\n' || r == '\r' || r == '\t' {
			return ' '
		}
		if r < 0x20 {
			return -1
		}
		return r
	}, s)
	return strings.TrimSpace(s)
}

// newID returns a random UUIDv4 string from the game's rng (deterministic in tests).
func (g *Game) newID() string {
	a, b := g.rng.Uint64(), g.rng.Uint64()
	a = (a &^ (0xf << 12)) | (0x4 << 12) // version 4
	b = (b &^ (0x3 << 62)) | (0x2 << 62) // variant 10
	return fmt.Sprintf("%08x-%04x-%04x-%04x-%012x", a>>32, (a>>16)&0xffff, a&0xffff, b>>48, b&0xffffffffffff)
}
