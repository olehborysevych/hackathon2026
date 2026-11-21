// Package agent is the seam between the game hub and whatever plays a non-human seat.
//
// The hub calls Agent.Act once per seat per phase with a deadline of phase end − 3s, then turns
// the reply into ordinary submissions with Resolve: the same submit path a human uses. Any
// error, timeout or malformed reply becomes a skip, never a stuck phase (invariant 6).
//
// M2 ships Stub (canned text, no LLM). M3 swaps in an adapter over internal/ai.
package agent

import (
	"context"
	"strings"
	"time"
	"unicode/utf8"

	"hackathon2026/server/internal/game"
	"hackathon2026/server/internal/ws"
)

// Request is what a seat may know when it acts: only public table content.
type Request struct {
	GameID     string
	Seat       int
	Alias      string
	Kind       game.SeatKind // ai or bot_human
	ModelID    string
	Phase      game.Phase
	Round      int
	Deadline   time.Time     // the agent's own deadline (phase end − margin)
	Questions  []ws.Question // ANSWER: questions to answer
	Candidates []int         // VOTE: seats it may vote for
	Seats      []ws.PublicSeat
	Transcript []ws.PhaseReveal
}

// Item is one slot: the intro, the question, or the answer to QuestionID.
type Item struct {
	QuestionID string
	Text       string
	Skip       bool
}

type Reply struct {
	Items []Item
	Vote  *int // VOTE only; nil = no vote
}

type Agent interface {
	Act(ctx context.Context, req Request) (Reply, error)
}

// DeadlineMargin is how long before the phase deadline an agent must be done (CLAUDE.md).
const DeadlineMargin = 3 * time.Second

// Resolve turns a reply (or a failure) into submissions that the game will accept:
// every expected slot gets exactly one entry, failures and blanks are skips, over-long text is
// cut to the limit, unknown question ids are dropped. vote is nil when there is no valid vote.
func Resolve(req Request, rep Reply, err error) (subs []game.Submission, vote *int) {
	if req.Phase == game.Vote {
		if err != nil || rep.Vote == nil {
			return nil, nil
		}
		for _, c := range req.Candidates {
			if c == *rep.Vote {
				v := c
				return nil, &v
			}
		}
		return nil, nil
	}

	var kind game.MsgKind
	limit := 0
	slots := []string{""}
	switch req.Phase {
	case game.Intro:
		kind, limit = game.MsgIntro, game.MaxIntro
	case game.Ask:
		kind, limit = game.MsgQuestion, game.MaxAsk
	case game.Answer:
		kind, limit = game.MsgAnswer, game.MaxAnswer
		slots = slots[:0]
		for _, q := range req.Questions {
			slots = append(slots, q.ID)
		}
	default:
		return nil, nil
	}

	given := map[string]Item{}
	if err == nil {
		for _, it := range rep.Items {
			if req.Phase != game.Answer {
				it.QuestionID = ""
			}
			if _, dup := given[it.QuestionID]; !dup {
				given[it.QuestionID] = it
			}
		}
	}
	for _, qid := range slots {
		it, ok := given[qid]
		text := truncate(strings.TrimSpace(it.Text), limit)
		skip := !ok || it.Skip || text == ""
		if skip {
			text = ""
		}
		subs = append(subs, game.Submission{
			Seat: req.Seat, Phase: req.Phase, Round: req.Round, Kind: kind,
			QuestionID: qid, Text: text, Skip: skip,
		})
	}
	return subs, nil
}

func truncate(s string, n int) string {
	if utf8.RuneCountInString(s) <= n {
		return s
	}
	r := []rune(s)
	return strings.TrimSpace(string(r[:n]))
}
