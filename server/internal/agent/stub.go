package agent

import (
	"context"
	"hash/fnv"
	"math/rand/v2"
	"sync"
	"time"

	"hackathon2026/server/internal/game"
)

// Stub plays a seat with canned lines and a skip rate. No LLM, no network. It also drives
// bot_human seats in dev, so one person can test alone.
type Stub struct {
	SkipRate float64       // chance to skip each slot
	MinDelay time.Duration // simulated thinking time; harmless because submissions are sealed
	MaxDelay time.Duration

	mu  sync.Mutex
	rng *rand.Rand
}

func NewStub(seed uint64) *Stub {
	return &Stub{SkipRate: 0.12, MinDelay: 300 * time.Millisecond, MaxDelay: 2500 * time.Millisecond,
		rng: rand.New(rand.NewPCG(seed, seed^0x9e3779b97f4a7c15))}
}

var (
	stubIntros = []string{
		"hey all. just got here, still figuring out how this works lol",
		"hi! night owl, coffee addict, bad at games like this",
		"yo. here for the snacks mostly",
		"hello hello, first time playing, be nice",
		"evening. i fix bikes and i talk too much",
		"hi im here because my friend dared me to",
	}
	stubQuestions = []string{
		"whats the last thing you ate?",
		"what did you do this morning?",
		"cats or dogs and why",
		"what's the worst job you ever had?",
		"what song is stuck in your head rn",
		"where would you go if you could leave right now?",
		"what's on the table in front of you?",
	}
	stubAnswers = []string{
		"idk honestly",
		"cold pizza, no regrets",
		"slept in and then panicked",
		"dogs. cats judge me",
		"hard to say, ask me later",
		"lol pass",
		"a cup of tea that went cold an hour ago",
		"washing dishes at a wedding, never again",
		"somewhere warm with no wifi",
		"that one song from the ad, you know the one",
	}
)

// pick spreads seats over the list: the same game, phase and question gets a random starting
// line, and each seat takes the next one, so two stub seats never say the same thing at once.
func (s *Stub) pick(list []string, req Request, qid string) string {
	h := fnv.New64a()
	h.Write([]byte(req.GameID + "|" + string(req.Phase) + "|" + qid))
	h.Write([]byte{byte(req.Round)})
	return list[(h.Sum64()+uint64(req.Seat))%uint64(len(list))]
}

func (s *Stub) chance(p float64) bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.rng.Float64() < p
}

func (s *Stub) delay() time.Duration {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.MaxDelay <= s.MinDelay {
		return s.MinDelay
	}
	return s.MinDelay + time.Duration(s.rng.Int64N(int64(s.MaxDelay-s.MinDelay)))
}

func (s *Stub) Act(ctx context.Context, req Request) (Reply, error) {
	select {
	case <-ctx.Done():
		return Reply{}, ctx.Err()
	case <-time.After(s.delay()):
	}

	var rep Reply
	switch req.Phase {
	case game.Intro:
		rep.Items = []Item{{Text: s.pick(stubIntros, req, ""), Skip: s.chance(s.SkipRate)}}
	case game.Ask:
		rep.Items = []Item{{Text: s.pick(stubQuestions, req, ""), Skip: s.chance(s.SkipRate)}}
	case game.Answer:
		for _, q := range req.Questions {
			rep.Items = append(rep.Items, Item{QuestionID: q.ID, Text: s.pick(stubAnswers, req, q.ID), Skip: s.chance(s.SkipRate)})
		}
	case game.Vote:
		if len(req.Candidates) > 0 {
			s.mu.Lock()
			v := req.Candidates[s.rng.IntN(len(req.Candidates))]
			s.mu.Unlock()
			rep.Vote = &v
		}
	}
	return rep, nil
}
