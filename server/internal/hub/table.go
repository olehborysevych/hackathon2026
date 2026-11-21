package hub

import (
	"context"
	"encoding/json"
	"errors"
	"log/slog"
	"strings"
	"time"

	"hackathon2026/server/internal/agent"
	"hackathon2026/server/internal/game"
	"hackathon2026/server/internal/ws"
)

// table is the actor for one game. Only its own goroutine touches g.
type table struct {
	h      *Hub
	id     string
	g      *game.Game
	seatOf map[string]int // user id → seat (humans only); read-only after creation
	userAt map[int]string // seat → user id
	inbox  chan any
	done   chan struct{}

	persist chan any

	timer       *time.Timer
	phaseCtx    context.Context
	phaseCancel context.CancelFunc
}

type inMsg struct {
	client *Client
	env    ws.Envelope
}

type agentDone struct {
	run     game.RunAgent
	req     agent.Request
	reply   agent.Reply
	err     error
	latency time.Duration
}

func newTable(h *Hub, g *game.Game) *table {
	t := &table{
		h: h, id: g.ID, g: g,
		seatOf: map[string]int{}, userAt: map[int]string{},
		inbox: make(chan any, 128), done: make(chan struct{}),
		persist: make(chan any, 1024),
	}
	for _, s := range g.Seats() {
		if s.Kind == game.KindHuman {
			t.seatOf[s.UserID] = s.Index
			t.userAt[s.Index] = s.UserID
		}
	}
	return t
}

// post hands a message to the table without blocking on a finished table.
func (t *table) post(m any) {
	select {
	case t.inbox <- m:
	case <-t.done:
	}
}

func (t *table) run() {
	defer close(t.done)
	go t.persister()
	defer close(t.persist)

	t.timer = time.NewTimer(time.Hour)
	t.timer.Stop()
	t.phaseCtx, t.phaseCancel = context.WithCancel(t.h.ctx)
	defer func() { t.phaseCancel() }()

	t.exec(t.g.Start(time.Now()))

	var retain <-chan time.Time
	for {
		if t.g.Phase() == game.Result && retain == nil {
			retain = time.After(t.h.opts.Retain)
		}
		select {
		case <-t.h.ctx.Done():
			return
		case <-retain:
			t.h.removeTable(t)
			return
		case <-t.timer.C:
			t.exec(t.g.Tick(time.Now()))
		case m := <-t.inbox:
			switch m := m.(type) {
			case inMsg:
				t.handle(m)
			case agentDone:
				t.agentDone(m)
			}
		}
	}
}

func (t *table) handle(m inMsg) {
	seat := t.seatOf[m.client.UserID]
	now := time.Now()
	var eff []game.Effect
	var err error
	switch m.env.Type {
	case ws.TGameResync:
		t.h.sendClient(m.client, ws.TGameSnapshot, t.g.Snapshot(seat, now))
		return
	case ws.TGameSubmit:
		var p ws.Submit
		if err = json.Unmarshal(m.env.Payload, &p); err == nil {
			eff, err = t.g.Submit(now, game.Submission{
				Seat: seat, Phase: game.Phase(p.Phase), Round: p.Round, Kind: game.MsgKind(p.Kind),
				QuestionID: p.QuestionID, Text: p.Text, FirstKeystrokeMs: p.FirstKeystrokeMs, EditCount: p.EditCount,
			})
		}
	case ws.TGameSkip:
		var p ws.Skip
		if err = json.Unmarshal(m.env.Payload, &p); err == nil {
			eff, err = t.g.Submit(now, game.Submission{
				Seat: seat, Phase: game.Phase(p.Phase), Round: p.Round, Kind: game.MsgKind(p.Kind),
				QuestionID: p.QuestionID, Skip: true,
			})
		}
	case ws.TVoteCast:
		var p ws.VoteCast
		if err = json.Unmarshal(m.env.Payload, &p); err == nil {
			eff, err = t.g.CastVote(now, seat, p.TargetSeat)
		}
	case ws.TNoteSet:
		var p ws.NoteSet
		if err = json.Unmarshal(m.env.Payload, &p); err == nil {
			eff, err = t.g.SetNote(now, seat, p.TargetSeat, p.Mark, p.Note)
		}
	}
	if err != nil {
		code := "bad_request"
		msg := "Invalid message."
		if errors.Is(err, game.ErrRejected) {
			code = "rejected"
			msg = strings.TrimPrefix(err.Error(), game.ErrRejected.Error()+": ")
		}
		t.h.sendError(m.client, code, msg)
		return
	}
	t.exec(eff)
}

func (t *table) exec(eff []game.Effect) {
	for _, e := range eff {
		switch e := e.(type) {
		case game.Broadcast:
			if e.Type == ws.TPhaseStart { // agents still working on the last phase are now stale
				t.phaseCancel()
				t.phaseCtx, t.phaseCancel = context.WithCancel(t.h.ctx)
			}
			frame, err := ws.Encode(e.Type, e.Payload)
			if err != nil {
				slog.Error("encode", "type", e.Type, "err", err)
				continue
			}
			for _, u := range t.userAt {
				t.h.sendUser(u, frame)
			}
		case game.SendTo:
			u, ok := t.userAt[e.Seat]
			if !ok {
				continue // agent seats get no frames
			}
			frame, err := ws.Encode(e.Type, e.Payload)
			if err != nil {
				slog.Error("encode", "type", e.Type, "err", err)
				continue
			}
			t.h.sendUser(u, frame)
		case game.SetTimer:
			t.timer.Stop()
			t.timer.Reset(time.Until(e.At))
		case game.RunAgent:
			t.startAgent(e)
		case game.Persist:
			select {
			case t.persist <- e.Record:
			default:
				slog.Error("persist queue full, dropping record", "game", t.id)
			}
		case game.Finished:
			t.timer.Stop()
			t.h.finished(t)
			slog.Info("game finished", "game", t.id)
		}
	}
}

func (t *table) startAgent(ra game.RunAgent) {
	s := t.g.Seats()[ra.Seat]
	deadline := ra.Deadline.Add(-t.h.opts.AgentMargin)
	req := agent.Request{
		GameID: t.id, Seat: ra.Seat, Alias: s.Alias, Kind: s.Kind, ModelID: s.ModelID,
		Phase: ra.Phase, Round: ra.Round, Deadline: deadline,
		Questions: ra.Questions, Candidates: ra.Candidates, Transcript: t.g.Reveals(),
	}
	for _, ps := range t.g.Snapshot(ra.Seat, time.Now()).Seats {
		req.Seats = append(req.Seats, ps)
	}
	ctx, cancel := context.WithDeadline(t.phaseCtx, deadline)
	go func() {
		defer cancel()
		start := time.Now()
		rep, err := t.h.opts.Agent.Act(ctx, req)
		if err == nil && ctx.Err() != nil {
			err = ctx.Err() // a reply after the deadline counts as a timeout
		}
		t.post(agentDone{run: ra, req: req, reply: rep, err: err, latency: time.Since(start)})
	}()
}

func (t *table) agentDone(d agentDone) {
	subs, vote := agent.Resolve(d.req, d.reply, d.err)
	s := t.g.Seats()[d.run.Seat]
	if s.Kind == game.KindAI {
		final, _ := json.Marshal(struct {
			Items []game.Submission `json:"items,omitempty"`
			Vote  *int              `json:"vote,omitempty"`
		}{subs, vote})
		rec := agent.CallRecord{
			GameID: t.id, Seat: d.run.Seat, Purpose: purpose(d.run.Phase), Provider: s.Provider, ModelID: s.ModelID,
			PromptVersion: t.h.opts.PromptVersion, Final: string(final), LatencyMs: int(d.latency.Milliseconds()), CreatedAt: time.Now(),
		}
		if d.err != nil {
			rec.Error = d.err.Error()
		}
		t.exec([]game.Effect{game.Persist{Record: rec}})
	}
	if t.g.Phase() != d.run.Phase || t.g.Round() != d.run.Round {
		return // phase already over: the game recorded the missing slots as skips
	}
	now := time.Now()
	for _, sub := range subs {
		eff, err := t.g.Submit(now, sub)
		if err != nil {
			slog.Warn("agent submission rejected", "game", t.id, "seat", sub.Seat, "err", err)
			continue
		}
		t.exec(eff)
	}
	if vote != nil {
		if eff, err := t.g.CastVote(now, d.run.Seat, *vote); err == nil {
			t.exec(eff)
		}
	}
}

func purpose(p game.Phase) string {
	switch p {
	case game.Intro:
		return "intro"
	case game.Ask:
		return "ask"
	case game.Answer:
		return "answer"
	case game.Vote:
		return "vote"
	}
	return strings.ToLower(string(p))
}

// persister writes records in order, off the game goroutine, so a slow database never delays
// a phase. It drains the queue after the game exits.
func (t *table) persister() {
	st := t.h.opts.Store
	for rec := range t.persist {
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		var err error
		switch r := rec.(type) {
		case game.GameRecord:
			r.Difficulty, r.PromptVersion = t.h.opts.Difficulty, t.h.opts.PromptVersion
			err = st.CreateGame(ctx, r)
		case game.MessageRecord:
			err = st.InsertMessage(ctx, r)
		case game.VoteRecord:
			err = st.InsertVote(ctx, r)
		case game.NoteRecord:
			err = st.InsertNote(ctx, r)
		case game.FinishRecord:
			err = st.FinishGame(ctx, r)
		case agent.CallRecord:
			err = st.InsertAICall(ctx, r)
		}
		cancel()
		if err != nil {
			slog.Error("persist", "game", t.id, "record", typeName(rec), "err", err)
		}
	}
}

func typeName(v any) string {
	switch v.(type) {
	case game.GameRecord:
		return "game"
	case game.MessageRecord:
		return "message"
	case game.VoteRecord:
		return "vote"
	case game.NoteRecord:
		return "note"
	case game.FinishRecord:
		return "finish"
	case agent.CallRecord:
		return "ai_call"
	}
	return "unknown"
}
