// Package hub runs the lobby and the live games (SPEC §5): a FIFO matchmaker, one goroutine per
// game with an inbox channel and its timer, fan-out to WebSocket clients, and persistence.
//
// The hub never knows usernames: clients are identified by user id only, and seats by alias.
package hub

import (
	"context"
	crand "crypto/rand"
	"encoding/binary"
	"encoding/json"
	"fmt"
	"log/slog"
	"math/rand/v2"
	"sync"
	"time"

	"hackathon2026/server/internal/agent"
	"hackathon2026/server/internal/game"
	"hackathon2026/server/internal/ws"
)

// Store is the persistence the hub needs. *store.Store implements it.
type Store interface {
	CreateGame(ctx context.Context, r game.GameRecord) error
	InsertMessage(ctx context.Context, r game.MessageRecord) error
	InsertVote(ctx context.Context, r game.VoteRecord) error
	InsertNote(ctx context.Context, r game.NoteRecord) error
	FinishGame(ctx context.Context, r game.FinishRecord) error
	InsertAICall(ctx context.Context, r agent.CallRecord) error
}

type Options struct {
	Config        game.Config
	BotHumanAfter time.Duration // >0: fill missing human seats with bot_human after this wait (dev only)
	Agent         agent.Agent   // plays ai and bot_human seats
	Store         Store
	AgentMargin   time.Duration // agents must finish this long before the deadline (default 3s)
	Retain        time.Duration // keep finished games in memory for resync (default 10m)
	AIProvider    string        // recorded on ai seats (default "stub")
	AIModel       string        // recorded on ai seats and shown at RESULT (default "stub")
	Difficulty    string        // recorded on games (default "stub")
	PromptVersion string        // recorded on games and ai calls (default "stub")
	// AISeats returns the specs (provider, model, persona) for a new game's AI seats. Nil = every
	// AI seat gets AIProvider/AIModel and no persona.
	AISeats func(n int) []game.SeatSpec
}

type Hub struct {
	opts Options
	ctx  context.Context

	mu      sync.Mutex
	clients map[string]map[*Client]struct{} // user id → open connections
	queue   []queued
	active  map[string]*table // user id → running game
	tables  map[string]*table // game id → game (running or retained)
}

type queued struct {
	userID string
	since  time.Time
}

// Client is one connection. The hub writes frames to Send; the connection's writer drains it.
type Client struct {
	UserID string
	Send   chan []byte
	once   sync.Once
	closed chan struct{}
}

func (c *Client) Closed() <-chan struct{} { return c.closed }
func (c *Client) close()                  { c.once.Do(func() { close(c.closed) }) }

func New(ctx context.Context, o Options) *Hub {
	if o.AgentMargin == 0 {
		o.AgentMargin = agent.DeadlineMargin
	}
	if o.Retain == 0 {
		o.Retain = 10 * time.Minute
	}
	if o.AIProvider == "" {
		o.AIProvider = "stub"
	}
	if o.AIModel == "" {
		o.AIModel = "stub"
	}
	if o.Difficulty == "" {
		o.Difficulty = "stub"
	}
	if o.PromptVersion == "" {
		o.PromptVersion = "stub"
	}
	if o.Agent == nil {
		o.Agent = agent.NewStub(seed())
	}
	if o.Store == nil {
		o.Store = nopStore{}
	}
	h := &Hub{
		opts: o, ctx: ctx,
		clients: map[string]map[*Client]struct{}{},
		active:  map[string]*table{}, tables: map[string]*table{},
	}
	if o.BotHumanAfter > 0 {
		go h.botFiller()
	}
	return h
}

// Connect registers a connection for a signed-in user.
func (h *Hub) Connect(userID string) *Client {
	c := &Client{UserID: userID, Send: make(chan []byte, 64), closed: make(chan struct{})}
	h.mu.Lock()
	defer h.mu.Unlock()
	if h.clients[userID] == nil {
		h.clients[userID] = map[*Client]struct{}{}
	}
	h.clients[userID][c] = struct{}{}
	return c
}

// Disconnect drops a connection. A user with no connections left leaves the queue; a seated
// user keeps the seat and can come back with game.resync.
func (h *Hub) Disconnect(c *Client) {
	c.close()
	h.mu.Lock()
	defer h.mu.Unlock()
	set := h.clients[c.UserID]
	delete(set, c)
	if len(set) == 0 {
		delete(h.clients, c.UserID)
		if h.removeQueuedLocked(c.UserID) {
			h.queueStatusLocked()
		}
	}
}

// Handle routes one client frame.
func (h *Hub) Handle(c *Client, env ws.Envelope) {
	switch env.Type {
	case ws.TQueueJoin:
		h.join(c)
	case ws.TQueueLeave:
		h.mu.Lock()
		h.removeQueuedLocked(c.UserID)
		h.queueStatusLocked()
		h.mu.Unlock()
		h.sendClient(c, ws.TQueueStatus, ws.QueueStatus{Queued: false, HumansNeeded: h.opts.Config.HumansPerGame})
	case ws.TGameSubmit, ws.TGameSkip, ws.TVoteCast, ws.TNoteSet, ws.TGameResync:
		var ref struct {
			GameID string `json:"game_id"`
		}
		if err := json.Unmarshal(env.Payload, &ref); err != nil || ref.GameID == "" {
			h.sendError(c, "bad_request", "game_id is required")
			return
		}
		h.mu.Lock()
		t := h.tables[ref.GameID]
		h.mu.Unlock()
		if t == nil {
			h.sendError(c, "game_not_found", "That game is over or does not exist.")
			return
		}
		if _, seated := t.seatOf[c.UserID]; !seated {
			h.sendError(c, "game_not_found", "That game is over or does not exist.")
			return
		}
		t.post(inMsg{client: c, env: env})
	default:
		h.sendError(c, "bad_request", "unknown message type")
	}
}

func (h *Hub) join(c *Client) {
	h.mu.Lock()
	if t := h.active[c.UserID]; t != nil {
		h.mu.Unlock()
		// Already seated: send the table instead of queueing twice.
		p, _ := json.Marshal(ws.Resync{GameID: t.id})
		t.post(inMsg{client: c, env: ws.Envelope{Type: ws.TGameResync, Payload: p}})
		return
	}
	inQueue := false
	for _, q := range h.queue {
		if q.userID == c.UserID {
			inQueue = true
		}
	}
	if !inQueue {
		h.queue = append(h.queue, queued{userID: c.UserID, since: time.Now()})
	}
	h.matchLocked()
	h.queueStatusLocked()
	h.mu.Unlock()
}

func (h *Hub) removeQueuedLocked(userID string) bool {
	for i, q := range h.queue {
		if q.userID == userID {
			h.queue = append(h.queue[:i], h.queue[i+1:]...)
			return true
		}
	}
	return false
}

// matchLocked starts games while enough humans are queued (FIFO).
func (h *Hub) matchLocked() {
	n := h.opts.Config.HumansPerGame
	for len(h.queue) >= n {
		users := make([]string, n)
		for i := range users {
			users[i] = h.queue[i].userID
		}
		h.queue = h.queue[n:]
		h.startLocked(users, 0)
	}
}

// botFiller seats bot_human players once the oldest queued human has waited long enough.
func (h *Hub) botFiller() {
	tick := time.NewTicker(250 * time.Millisecond)
	defer tick.Stop()
	for {
		select {
		case <-h.ctx.Done():
			return
		case <-tick.C:
		}
		h.mu.Lock()
		if len(h.queue) > 0 && time.Since(h.queue[0].since) >= h.opts.BotHumanAfter {
			n := min(len(h.queue), h.opts.Config.HumansPerGame)
			users := make([]string, n)
			for i := range users {
				users[i] = h.queue[i].userID
			}
			h.queue = h.queue[n:]
			h.startLocked(users, h.opts.Config.HumansPerGame-n)
			h.queueStatusLocked()
		}
		h.mu.Unlock()
	}
}

func (h *Hub) queueStatusLocked() {
	st := ws.QueueStatus{Queued: true, HumansWaiting: len(h.queue), HumansNeeded: h.opts.Config.HumansPerGame}
	frame, err := ws.Encode(ws.TQueueStatus, st)
	if err != nil {
		return
	}
	for _, q := range h.queue {
		for c := range h.clients[q.userID] {
			h.push(c, frame)
		}
	}
}

func (h *Hub) startLocked(users []string, bots int) {
	cfg := h.opts.Config
	var specs []game.SeatSpec
	for _, u := range users {
		specs = append(specs, game.SeatSpec{Kind: game.KindHuman, UserID: u})
	}
	for i := 0; i < bots; i++ {
		specs = append(specs, game.SeatSpec{Kind: game.KindBotHuman})
	}
	var ai []game.SeatSpec
	if h.opts.AISeats != nil {
		ai = h.opts.AISeats(cfg.AIPerGame)
	}
	for i := 0; i < cfg.AIPerGame; i++ {
		spec := game.SeatSpec{Provider: h.opts.AIProvider, ModelID: h.opts.AIModel}
		if i < len(ai) {
			spec = ai[i]
		}
		spec.Kind, spec.UserID = game.KindAI, ""
		specs = append(specs, spec)
	}
	id := newUUID()
	g := game.New(id, cfg, specs, rand.New(rand.NewPCG(seed(), seed())))
	t := newTable(h, g)
	h.tables[id] = t
	for _, u := range users {
		h.active[u] = t
	}
	slog.Info("game started", "game", id, "humans", len(users), "bot_humans", bots, "ai", cfg.AIPerGame)
	go t.run()
}

// sendUser delivers a frame to every connection of a user.
func (h *Hub) sendUser(userID string, frame []byte) {
	h.mu.Lock()
	defer h.mu.Unlock()
	for c := range h.clients[userID] {
		h.push(c, frame)
	}
}

// push never blocks: a client that cannot keep up is dropped and will resync on reconnect.
func (h *Hub) push(c *Client, frame []byte) {
	select {
	case <-c.closed:
	case c.Send <- frame:
	default:
		slog.Warn("dropping slow client", "user", c.UserID)
		c.close()
	}
}

func (h *Hub) sendClient(c *Client, typ string, payload any) {
	frame, err := ws.Encode(typ, payload)
	if err != nil {
		slog.Error("encode", "type", typ, "err", err)
		return
	}
	h.push(c, frame)
}

func (h *Hub) sendError(c *Client, code, msg string) {
	h.sendClient(c, ws.TError, ws.Error{Code: code, Message: msg})
}

// finished is called by a table when its game reaches RESULT: players may queue again.
func (h *Hub) finished(t *table) {
	h.mu.Lock()
	defer h.mu.Unlock()
	for u := range t.seatOf {
		if h.active[u] == t {
			delete(h.active, u)
		}
	}
}

func (h *Hub) removeTable(t *table) {
	h.mu.Lock()
	defer h.mu.Unlock()
	if h.tables[t.id] == t {
		delete(h.tables, t.id)
	}
	for u := range t.seatOf {
		if h.active[u] == t {
			delete(h.active, u)
		}
	}
}

// ActiveGame returns the id of the user's running game, if any.
func (h *Hub) ActiveGame(userID string) string {
	h.mu.Lock()
	defer h.mu.Unlock()
	if t := h.active[userID]; t != nil {
		return t.id
	}
	return ""
}

func seed() uint64 {
	var b [8]byte
	_, _ = crand.Read(b[:])
	return binary.LittleEndian.Uint64(b[:])
}

func newUUID() string {
	var b [16]byte
	_, _ = crand.Read(b[:])
	b[6] = (b[6] & 0x0f) | 0x40
	b[8] = (b[8] & 0x3f) | 0x80
	return fmt.Sprintf("%x-%x-%x-%x-%x", b[0:4], b[4:6], b[6:8], b[8:10], b[10:])
}

type nopStore struct{}

func (nopStore) CreateGame(context.Context, game.GameRecord) error       { return nil }
func (nopStore) InsertMessage(context.Context, game.MessageRecord) error { return nil }
func (nopStore) InsertVote(context.Context, game.VoteRecord) error       { return nil }
func (nopStore) InsertNote(context.Context, game.NoteRecord) error       { return nil }
func (nopStore) FinishGame(context.Context, game.FinishRecord) error     { return nil }
func (nopStore) InsertAICall(context.Context, agent.CallRecord) error    { return nil }
