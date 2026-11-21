// Package ws holds the WebSocket envelope and payload types (SPEC §7). Pure data, no I/O.
//
// Everything a server→client payload can carry is declared here, so the invariant tests can
// reason about it: before game.result no payload has a seat kind, model, provider, user id,
// username, or another seat's submission status or timing.
package ws

import "encoding/json"

// Envelope is the frame on the wire: {"type": "...", "payload": {...}}.
type Envelope struct {
	Type    string          `json:"type"`
	Payload json.RawMessage `json:"payload,omitempty"`
}

// Encode builds a frame from a typed payload.
func Encode(typ string, payload any) ([]byte, error) {
	p, err := json.Marshal(payload)
	if err != nil {
		return nil, err
	}
	return json.Marshal(Envelope{Type: typ, Payload: p})
}

// Client → server message types.
const (
	TQueueJoin  = "queue.join"
	TQueueLeave = "queue.leave"
	TGameSubmit = "game.submit"
	TGameSkip   = "game.skip"
	TNoteSet    = "note.set"
	TVoteCast   = "vote.cast"
	TGameResync = "game.resync"
)

// Server → client message types.
const (
	TQueueStatus  = "queue.status"
	TGameStart    = "game.start"
	TPhaseStart   = "phase.start"
	TPhaseReveal  = "phase.reveal"
	TSubmitAck    = "submit.ack"
	TVoteAck      = "vote.ack"
	TNoteAck      = "note.ack"
	TGameResult   = "game.result"
	TGameSnapshot = "game.snapshot"
	TError        = "error"
)

// ---- client → server payloads ----

type Submit struct {
	GameID           string `json:"game_id"`
	Phase            string `json:"phase"`
	Round            int    `json:"round"`
	Kind             string `json:"kind"`
	QuestionID       string `json:"question_id,omitempty"`
	Text             string `json:"text"`
	FirstKeystrokeMs *int   `json:"first_keystroke_ms,omitempty"`
	EditCount        *int   `json:"edit_count,omitempty"`
}

type Skip struct {
	GameID     string `json:"game_id"`
	Phase      string `json:"phase"`
	Round      int    `json:"round"`
	Kind       string `json:"kind"`
	QuestionID string `json:"question_id,omitempty"`
}

type NoteSet struct {
	GameID     string `json:"game_id"`
	TargetSeat int    `json:"target_seat"`
	Mark       string `json:"mark"`
	Note       string `json:"note,omitempty"`
}

type VoteCast struct {
	GameID     string `json:"game_id"`
	TargetSeat int    `json:"target_seat"`
}

type Resync struct {
	GameID string `json:"game_id"`
}

// ---- server → client payloads ----

type QueueStatus struct {
	Queued        bool `json:"queued"`
	HumansWaiting int  `json:"humans_waiting"`
	HumansNeeded  int  `json:"humans_needed"`
}

// PublicSeat is what every player may know about a seat before the result.
type PublicSeat struct {
	Seat       int    `json:"seat"`
	Alias      string `json:"alias"`
	SpriteSeed int    `json:"sprite_seed"`
}

// PublicConfig is the table config shown to players.
type PublicConfig struct {
	Seats     int `json:"seats"`
	Rounds    int `json:"rounds"`
	MaxIntro  int `json:"max_intro"`
	MaxAsk    int `json:"max_ask"`
	MaxAnswer int `json:"max_answer"`
}

type GameStart struct {
	GameID string       `json:"game_id"`
	MySeat int          `json:"my_seat"`
	Seats  []PublicSeat `json:"seats"`
	Config PublicConfig `json:"config"`
}

type Question struct {
	ID   string `json:"id"`
	Seat int    `json:"seat"`
	Text string `json:"text"`
}

type PhaseStart struct {
	Phase      string     `json:"phase"`
	Round      int        `json:"round"`
	DeadlineTS int64      `json:"deadline_ts"` // unix ms
	ServerTS   int64      `json:"server_ts"`   // unix ms, lets the client correct clock skew
	Questions  []Question `json:"questions,omitempty"`
}

// RevealItem.Kind is the message kind (intro|question|answer), never a seat kind.
// Text is null both for a skip and for a missing submission, so the two look the same.
type RevealItem struct {
	ID         string  `json:"id"`
	Seat       int     `json:"seat"`
	Kind       string  `json:"kind"`
	QuestionID string  `json:"question_id,omitempty"`
	Text       *string `json:"text"`
}

type PhaseReveal struct {
	Phase string       `json:"phase"`
	Round int          `json:"round"`
	Items []RevealItem `json:"items"`
}

type SubmitAck struct {
	Phase      string `json:"phase"`
	Round      int    `json:"round"`
	Kind       string `json:"kind"`
	QuestionID string `json:"question_id,omitempty"`
	Skipped    bool   `json:"skipped"`
}

type VoteAck struct {
	TargetSeat int `json:"target_seat"`
}

type Note struct {
	TargetSeat int    `json:"target_seat"`
	Mark       string `json:"mark"`
	Note       string `json:"note,omitempty"`
}

type ResultSeat struct {
	Seat       int    `json:"seat"`
	Alias      string `json:"alias"`
	SpriteSeed int    `json:"sprite_seed"`
	Kind       string `json:"kind"`
	ModelID    string `json:"model_id,omitempty"`
}

type VoteArrow struct {
	Voter  int `json:"voter"`
	Target int `json:"target"`
}

type SeatResult struct {
	Seat        int  `json:"seat"`
	Points      int  `json:"points"`
	FoundOther  bool `json:"found_other"`
	WasFound    bool `json:"was_found"`
	Mutual      bool `json:"mutual"`
	VotesStolen *int `json:"votes_stolen,omitempty"`
}

type GameResult struct {
	GameID  string       `json:"game_id"`
	Seats   []ResultSeat `json:"seats"`
	Votes   []VoteArrow  `json:"votes"`
	Results []SeatResult `json:"results"`
}

// Snapshot is the full state visible to one player, sent on game.resync and on reconnect.
// Mine lists only the player's own submissions in the current phase.
type Snapshot struct {
	GameID     string        `json:"game_id"`
	MySeat     int           `json:"my_seat"`
	Seats      []PublicSeat  `json:"seats"`
	Config     PublicConfig  `json:"config"`
	Phase      string        `json:"phase"`
	Round      int           `json:"round"`
	DeadlineTS int64         `json:"deadline_ts"`
	ServerTS   int64         `json:"server_ts"`
	Questions  []Question    `json:"questions,omitempty"`
	Reveals    []PhaseReveal `json:"reveals"`
	Mine       []SubmitAck   `json:"mine"`
	MyVote     *int          `json:"my_vote,omitempty"`
	Notes      []Note        `json:"notes"`
	Result     *GameResult   `json:"result,omitempty"`
}

type Error struct {
	Code    string `json:"code"`
	Message string `json:"message"`
}
