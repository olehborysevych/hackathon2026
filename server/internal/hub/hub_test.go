package hub

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/coder/websocket"

	"hackathon2026/server/internal/agent"
	"hackathon2026/server/internal/game"
	"hackathon2026/server/internal/ws"
)

const (
	alice = "aaaaaaaa-1111-4111-8111-aaaaaaaaaaaa"
	bob   = "bbbbbbbb-2222-4222-8222-bbbbbbbbbbbb"
	carol = "cccccccc-3333-4333-8333-cccccccccccc"
)

type fakeStore struct {
	mu       sync.Mutex
	games    []game.GameRecord
	messages []game.MessageRecord
	votes    []game.VoteRecord
	notes    []game.NoteRecord
	finishes []game.FinishRecord
	aiCalls  []agent.CallRecord
}

func (f *fakeStore) CreateGame(_ context.Context, r game.GameRecord) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.games = append(f.games, r)
	return nil
}
func (f *fakeStore) InsertMessage(_ context.Context, r game.MessageRecord) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.messages = append(f.messages, r)
	return nil
}
func (f *fakeStore) InsertVote(_ context.Context, r game.VoteRecord) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.votes = append(f.votes, r)
	return nil
}
func (f *fakeStore) InsertNote(_ context.Context, r game.NoteRecord) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.notes = append(f.notes, r)
	return nil
}
func (f *fakeStore) FinishGame(_ context.Context, r game.FinishRecord) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.finishes = append(f.finishes, r)
	return nil
}
func (f *fakeStore) InsertAICall(_ context.Context, r agent.CallRecord) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.aiCalls = append(f.aiCalls, r)
	return nil
}

func fastConfig(d time.Duration) game.Config {
	return game.Config{HumansPerGame: 2, AIPerGame: 3, Rounds: 1, TIntro: d, TAsk: d, TAnswer: d, TReveal: d / 3, TVote: d}
}

func newTestHub(t *testing.T, o Options) (*Hub, *fakeStore) {
	t.Helper()
	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)
	st := &fakeStore{}
	o.Store = st
	if o.Agent == nil {
		s := agent.NewStub(1)
		s.MinDelay, s.MaxDelay = 0, 5*time.Millisecond
		o.Agent = s
	}
	if o.AgentMargin == 0 {
		o.AgentMargin = 20 * time.Millisecond
	}
	return New(ctx, o), st
}

func send(h *Hub, c *Client, typ string, payload any) {
	p, _ := json.Marshal(payload)
	h.Handle(c, ws.Envelope{Type: typ, Payload: p})
}

func recv(t *testing.T, c *Client, timeout time.Duration) (ws.Envelope, bool) {
	t.Helper()
	select {
	case f := <-c.Send:
		var env ws.Envelope
		if err := json.Unmarshal(f, &env); err != nil {
			t.Fatal(err)
		}
		return env, true
	case <-time.After(timeout):
		return ws.Envelope{}, false
	}
}

func waitFor(t *testing.T, c *Client, typ string, timeout time.Duration) ws.Envelope {
	t.Helper()
	deadline := time.Now().Add(timeout)
	for {
		env, ok := recv(t, c, time.Until(deadline))
		if !ok {
			t.Fatalf("no %s within %v", typ, timeout)
		}
		if env.Type == typ {
			return env
		}
	}
}

// player answers every phase until game.result and returns every frame it received.
func player(t *testing.T, h *Hub, c *Client, gameID string, voteFor func() int, done chan<- []ws.Envelope) {
	var frames []ws.Envelope
	defer func() { done <- frames }()
	for {
		var env ws.Envelope
		select {
		case f := <-c.Send:
			_ = json.Unmarshal(f, &env)
		case <-time.After(5 * time.Second):
			t.Error("player timed out")
			return
		}
		frames = append(frames, env)
		switch env.Type {
		case ws.TGameResult:
			return
		case ws.TPhaseStart:
			var ps ws.PhaseStart
			_ = json.Unmarshal(env.Payload, &ps)
			switch ps.Phase {
			case "INTRO":
				send(h, c, ws.TGameSubmit, ws.Submit{GameID: gameID, Phase: ps.Phase, Round: ps.Round, Kind: "intro", Text: "hi there"})
			case "ASK":
				send(h, c, ws.TGameSubmit, ws.Submit{GameID: gameID, Phase: ps.Phase, Round: ps.Round, Kind: "question", Text: "what's for lunch?"})
			case "ANSWER":
				for _, q := range ps.Questions {
					send(h, c, ws.TGameSkip, ws.Skip{GameID: gameID, Phase: ps.Phase, Round: ps.Round, Kind: "answer", QuestionID: q.ID})
				}
			case "VOTE":
				send(h, c, ws.TVoteCast, ws.VoteCast{GameID: gameID, TargetSeat: voteFor()})
			}
		}
	}
}

func checkFrames(t *testing.T, frames []ws.Envelope, forbidden []string) {
	t.Helper()
	for _, env := range frames {
		if env.Type == ws.TGameResult {
			return
		}
		s := string(env.Payload)
		for _, f := range forbidden {
			if strings.Contains(s, f) {
				t.Errorf("%s leaks %q: %s", env.Type, f, s)
			}
		}
		for _, key := range []string{`"model_id"`, `"user_id"`, `"provider"`, `"human"`, `"ai"`, `"bot_human"`} {
			if strings.Contains(s, key) {
				t.Errorf("%s contains %s: %s", env.Type, key, s)
			}
		}
	}
	t.Error("no game.result")
}

func TestTwoHumansPlayFullGame(t *testing.T) {
	h, st := newTestHub(t, Options{Config: fastConfig(400 * time.Millisecond)})
	a, b := h.Connect(alice), h.Connect(bob)
	send(h, a, ws.TQueueJoin, struct{}{})
	qs := waitFor(t, a, ws.TQueueStatus, time.Second)
	var status ws.QueueStatus
	_ = json.Unmarshal(qs.Payload, &status)
	if !status.Queued || status.HumansWaiting != 1 {
		t.Fatalf("queue status %+v", status)
	}
	send(h, b, ws.TQueueJoin, struct{}{})

	var startA, startB ws.GameStart
	_ = json.Unmarshal(waitFor(t, a, ws.TGameStart, time.Second).Payload, &startA)
	_ = json.Unmarshal(waitFor(t, b, ws.TGameStart, time.Second).Payload, &startB)
	if startA.GameID != startB.GameID || startA.MySeat == startB.MySeat || len(startA.Seats) != 5 {
		t.Fatalf("bad game.start %+v / %+v", startA, startB)
	}
	if h.ActiveGame(alice) != startA.GameID {
		t.Fatal("alice not marked in game")
	}

	da, db := make(chan []ws.Envelope, 1), make(chan []ws.Envelope, 1)
	go player(t, h, a, startA.GameID, func() int { return startB.MySeat }, da)
	go player(t, h, b, startB.GameID, func() int { return startA.MySeat }, db)
	fa, fb := <-da, <-db
	forbidden := []string{alice, bob, "stub"}
	checkFrames(t, fa, forbidden)
	checkFrames(t, fb, forbidden)

	var res ws.GameResult
	_ = json.Unmarshal(fa[len(fa)-1].Payload, &res)
	for _, r := range res.Results {
		if (r.Seat == startA.MySeat || r.Seat == startB.MySeat) && (!r.Mutual || r.Points != 3) {
			t.Errorf("humans voted for each other but got %+v", r)
		}
	}
	if h.ActiveGame(alice) != "" {
		t.Fatal("alice still marked in a finished game")
	}

	// Persistence: one game, every slot, every vote, one finish, one ai call per AI seat and phase.
	time.Sleep(50 * time.Millisecond)
	st.mu.Lock()
	defer st.mu.Unlock()
	if len(st.games) != 1 || len(st.finishes) != 1 {
		t.Fatalf("games=%d finishes=%d", len(st.games), len(st.finishes))
	}
	if len(st.votes) < 2 {
		t.Fatalf("votes=%d", len(st.votes))
	}
	for _, m := range st.messages {
		if m.PhaseStartedAt.IsZero() || (!m.Skipped && m.SubmittedAt == nil) {
			t.Fatalf("message without server timestamps: %+v", m)
		}
	}
	if len(st.aiCalls) != 3*4 { // INTRO, ASK, ANSWER, VOTE
		t.Fatalf("ai calls=%d", len(st.aiCalls))
	}
}

// Invariant 2 at the hub: a submission reaches nobody but the submitter until the phase ends.
func TestSubmissionsSealedAcrossClients(t *testing.T) {
	h, _ := newTestHub(t, Options{Config: fastConfig(5 * time.Second)})
	a, b := h.Connect(alice), h.Connect(bob)
	send(h, a, ws.TQueueJoin, struct{}{})
	send(h, b, ws.TQueueJoin, struct{}{})
	var start ws.GameStart
	_ = json.Unmarshal(waitFor(t, a, ws.TGameStart, time.Second).Payload, &start)
	waitFor(t, a, ws.TPhaseStart, time.Second)
	waitFor(t, b, ws.TPhaseStart, time.Second)

	time.Sleep(100 * time.Millisecond) // the stub AIs submit meanwhile
	send(h, a, ws.TGameSubmit, ws.Submit{GameID: start.GameID, Phase: "INTRO", Kind: "intro", Text: "hello"})
	if env := waitFor(t, a, ws.TSubmitAck, time.Second); env.Type != ws.TSubmitAck {
		t.Fatal("no ack")
	}
	if env, ok := recv(t, b, 200*time.Millisecond); ok {
		t.Fatalf("bob got %s after alice submitted: %s", env.Type, env.Payload)
	}
	send(h, b, ws.TGameSkip, ws.Skip{GameID: start.GameID, Phase: "INTRO", Kind: "intro"})
	// Bob's skip completes the phase: both see one reveal, then ASK.
	for _, c := range []*Client{a, b} {
		env := waitFor(t, c, ws.TPhaseReveal, time.Second)
		var rv ws.PhaseReveal
		_ = json.Unmarshal(env.Payload, &rv)
		if rv.Phase != "INTRO" || len(rv.Items) != 5 {
			t.Fatalf("reveal %+v", rv)
		}
		var ps ws.PhaseStart
		_ = json.Unmarshal(waitFor(t, c, ws.TPhaseStart, time.Second).Payload, &ps)
		if ps.Phase != "ASK" {
			t.Fatalf("next phase %s", ps.Phase)
		}
	}
}

func TestBotHumanFillsSeat(t *testing.T) {
	h, st := newTestHub(t, Options{Config: fastConfig(200 * time.Millisecond), BotHumanAfter: 100 * time.Millisecond})
	a := h.Connect(alice)
	send(h, a, ws.TQueueJoin, struct{}{})
	var start ws.GameStart
	_ = json.Unmarshal(waitFor(t, a, ws.TGameStart, 2*time.Second).Payload, &start)
	env := waitFor(t, a, ws.TGameResult, 5*time.Second)
	var res ws.GameResult
	_ = json.Unmarshal(env.Payload, &res)
	kinds := map[string]int{}
	for _, s := range res.Seats {
		kinds[s.Kind]++
	}
	if kinds["human"] != 1 || kinds["bot_human"] != 1 || kinds["ai"] != 3 {
		t.Fatalf("seat kinds %v", kinds)
	}
	time.Sleep(50 * time.Millisecond)
	st.mu.Lock()
	defer st.mu.Unlock()
	for _, c := range st.aiCalls { // bot_human seats are not AI and are not logged as AI calls
		for _, s := range st.games[0].Seats {
			if s.Index == c.Seat && s.Kind != game.KindAI {
				t.Fatalf("ai call logged for %s seat", s.Kind)
			}
		}
	}
}

type failingAgent struct{ hang bool }

func (f failingAgent) Act(ctx context.Context, _ agent.Request) (agent.Reply, error) {
	if f.hang {
		<-ctx.Done()
		return agent.Reply{}, ctx.Err()
	}
	return agent.Reply{}, errors.New("model unavailable")
}

// Invariant 6: a failing or hanging agent produces skips and the game still finishes.
func TestAgentFailureNeverBlocks(t *testing.T) {
	for _, ag := range []agent.Agent{failingAgent{}, failingAgent{hang: true}} {
		h, st := newTestHub(t, Options{Config: fastConfig(250 * time.Millisecond), Agent: ag, BotHumanAfter: 50 * time.Millisecond})
		a := h.Connect(alice)
		send(h, a, ws.TQueueJoin, struct{}{})
		waitFor(t, a, ws.TGameResult, 5*time.Second)
		time.Sleep(50 * time.Millisecond)
		st.mu.Lock()
		if len(st.aiCalls) == 0 || st.aiCalls[0].Error == "" {
			t.Fatalf("failure not logged: %+v", st.aiCalls)
		}
		st.mu.Unlock()
	}
}

func TestDisconnectLeavesQueue(t *testing.T) {
	h, _ := newTestHub(t, Options{Config: fastConfig(time.Second)})
	a := h.Connect(alice)
	send(h, a, ws.TQueueJoin, struct{}{})
	h.Disconnect(a)
	b := h.Connect(bob)
	send(h, b, ws.TQueueJoin, struct{}{})
	var st ws.QueueStatus
	_ = json.Unmarshal(waitFor(t, b, ws.TQueueStatus, time.Second).Payload, &st)
	if st.HumansWaiting != 1 {
		t.Fatalf("humans waiting %d", st.HumansWaiting)
	}
	if env, ok := recv(t, b, 100*time.Millisecond); ok && env.Type == ws.TGameStart {
		t.Fatal("game started with a disconnected user")
	}
}

func TestResyncAndRejoin(t *testing.T) {
	h, _ := newTestHub(t, Options{Config: fastConfig(5 * time.Second)})
	a, b, c := h.Connect(alice), h.Connect(bob), h.Connect(carol)
	send(h, a, ws.TQueueJoin, struct{}{})
	send(h, b, ws.TQueueJoin, struct{}{})
	var start ws.GameStart
	_ = json.Unmarshal(waitFor(t, a, ws.TGameStart, time.Second).Payload, &start)

	// A stranger cannot read the table.
	send(h, c, ws.TGameResync, ws.Resync{GameID: start.GameID})
	var e ws.Error
	_ = json.Unmarshal(waitFor(t, c, ws.TError, time.Second).Payload, &e)
	if e.Code != "game_not_found" {
		t.Fatalf("stranger got %+v", e)
	}

	// Reconnect: a new connection resyncs and gets its own seat.
	h.Disconnect(a)
	a2 := h.Connect(alice)
	send(h, a2, ws.TGameResync, ws.Resync{GameID: start.GameID})
	var snap ws.Snapshot
	_ = json.Unmarshal(waitFor(t, a2, ws.TGameSnapshot, time.Second).Payload, &snap)
	if snap.MySeat != start.MySeat || snap.Phase != "INTRO" || snap.DeadlineTS == 0 {
		t.Fatalf("snapshot %+v", snap)
	}
	// Joining the queue while seated sends the table back instead.
	send(h, a2, ws.TQueueJoin, struct{}{})
	waitFor(t, a2, ws.TGameSnapshot, time.Second)
	// The snapshot frame goes only to the requesting client.
	if env, ok := recv(t, b, 100*time.Millisecond); ok && env.Type == ws.TGameSnapshot {
		t.Fatal("snapshot sent to another player")
	}
}

func TestRejectedSubmitReturnsError(t *testing.T) {
	h, _ := newTestHub(t, Options{Config: fastConfig(5 * time.Second)})
	a, b := h.Connect(alice), h.Connect(bob)
	send(h, a, ws.TQueueJoin, struct{}{})
	send(h, b, ws.TQueueJoin, struct{}{})
	var start ws.GameStart
	_ = json.Unmarshal(waitFor(t, a, ws.TGameStart, time.Second).Payload, &start)
	send(h, a, ws.TGameSubmit, ws.Submit{GameID: start.GameID, Phase: "INTRO", Kind: "intro", Text: strings.Repeat("x", 500)})
	var e ws.Error
	_ = json.Unmarshal(waitFor(t, a, ws.TError, time.Second).Payload, &e)
	if e.Code != "rejected" || !strings.Contains(e.Message, "too long") {
		t.Fatalf("error %+v", e)
	}
}

func TestHandlerAuthAndOrigin(t *testing.T) {
	h, _ := newTestHub(t, Options{Config: fastConfig(time.Second)})
	authn := func(r *http.Request) (string, error) {
		c, err := r.Cookie("sid")
		if err != nil {
			return "", err
		}
		return c.Value, nil
	}
	srv := httptest.NewServer(h.Handler(authn, nil))
	defer srv.Close()
	url := "ws" + strings.TrimPrefix(srv.URL, "http")
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()

	dial := func(cookie, origin string) (*websocket.Conn, *http.Response, error) {
		hdr := http.Header{}
		if cookie != "" {
			hdr.Set("Cookie", "sid="+cookie)
		}
		if origin != "" {
			hdr.Set("Origin", origin)
		}
		return websocket.Dial(ctx, url, &websocket.DialOptions{HTTPHeader: hdr})
	}

	if _, resp, err := dial("", srv.URL); err == nil || resp == nil || resp.StatusCode != http.StatusUnauthorized {
		t.Fatalf("no cookie: err=%v resp=%v", err, resp)
	}
	if _, resp, err := dial(alice, "https://evil.example"); err == nil || resp == nil || resp.StatusCode != http.StatusForbidden {
		t.Fatalf("cross origin: err=%v resp=%v", err, resp)
	}
	conn, _, err := dial(alice, srv.URL)
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close(websocket.StatusNormalClosure, "")
	if err := conn.Write(ctx, websocket.MessageText, []byte(`{"type":"queue.join","payload":{}}`)); err != nil {
		t.Fatal(err)
	}
	_, data, err := conn.Read(ctx)
	if err != nil {
		t.Fatal(err)
	}
	var env ws.Envelope
	_ = json.Unmarshal(data, &env)
	if env.Type != ws.TQueueStatus {
		t.Fatalf("got %s", env.Type)
	}
}

// llmLikeAgent stands in for agent.LLM: it returns per-call records and flags like the real one.
type llmLikeAgent struct{}

func (llmLikeAgent) Act(_ context.Context, req agent.Request) (agent.Reply, error) {
	if req.Kind != game.KindAI {
		return agent.NewStub(1).Act(context.Background(), req)
	}
	rep := agent.Reply{Calls: []agent.CallRecord{{Purpose: "answer", Provider: "fake", ModelID: req.ModelID, Draft: "d", Final: "f", InputTokens: 9}}}
	switch req.Phase {
	case game.Answer:
		for _, q := range req.Questions {
			rep.Items = append(rep.Items, agent.Item{QuestionID: q.ID, Text: "As an AI I can't", BrokeCharacter: true})
		}
	case game.Vote:
		v := req.Candidates[0]
		rep.Vote = &v
	default:
		rep.Items = []agent.Item{{Text: "hello"}}
	}
	return rep, nil
}

// Personas and models reach the store, never a client frame (invariant 1); every LLM call is
// persisted with game and seat filled in (invariant 4); break flags reach messages (invariant 7).
func TestAISeatsPersonaPrivateAndCallsLogged(t *testing.T) {
	const marker = "persona-marker-7f3a"
	seats := func(n int) []game.SeatSpec {
		out := make([]game.SeatSpec, n)
		for i := range out {
			out[i] = game.SeatSpec{Provider: "openrouter", ModelID: "model-" + string(rune('a'+i)),
				Persona: json.RawMessage(`{"job":"` + marker + `"}`)}
		}
		return out
	}
	h, st := newTestHub(t, Options{Config: fastConfig(300 * time.Millisecond), Agent: llmLikeAgent{}, AISeats: seats, BotHumanAfter: 50 * time.Millisecond})
	a := h.Connect(alice)
	send(h, a, ws.TQueueJoin, struct{}{})
	var frames []ws.Envelope
	for {
		env, ok := recv(t, a, 5*time.Second)
		if !ok {
			t.Fatal("game did not finish")
		}
		frames = append(frames, env)
		if strings.Contains(string(env.Payload), marker) {
			t.Fatalf("%s leaks the persona: %s", env.Type, env.Payload)
		}
		if env.Type == ws.TGameResult {
			break
		}
	}
	checkFrames(t, frames, []string{marker, "model-a"})
	time.Sleep(50 * time.Millisecond)
	st.mu.Lock()
	defer st.mu.Unlock()
	models := map[string]bool{}
	for _, s := range st.games[0].Seats {
		if s.Kind == game.KindAI {
			if !strings.Contains(string(s.Persona), marker) {
				t.Errorf("seat %d persona not stored", s.Index)
			}
			models[s.ModelID] = true
		}
	}
	if len(models) != 3 {
		t.Errorf("per-seat models not applied: %v", models)
	}
	if len(st.aiCalls) != 3*4 {
		t.Fatalf("want one logged call per AI seat per phase, got %d", len(st.aiCalls))
	}
	for _, c := range st.aiCalls {
		if c.GameID == "" || c.InputTokens != 9 || c.Draft != "d" || c.CreatedAt.IsZero() {
			t.Fatalf("call record incomplete: %+v", c)
		}
	}
	broke := 0
	for _, m := range st.messages {
		if m.BrokeCharacter {
			broke++
			if m.Text != "As an AI I can't" {
				t.Errorf("break text altered: %q", m.Text)
			}
		}
	}
	if broke == 0 {
		t.Error("broke_character not persisted on messages")
	}
}
