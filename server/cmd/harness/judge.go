package main

import (
	"context"
	"encoding/json"
	"fmt"
	"math/rand/v2"
	"os"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"hackathon2026/prompts"
	"hackathon2026/server/internal/ai"
	"hackathon2026/server/internal/llm"
)

// Judge is the blind per-reply verdict. The judge never sees model, persona, config, or whether
// the reply is from the human calibration set: only what a player at the table would see.
type Judge struct {
	PHuman              int    `json:"p_human"`
	AssistantVoice      bool   `json:"assistant_voice"`
	OverKnowledge       bool   `json:"over_knowledge"`
	Echo                bool   `json:"echo"`
	InjectionCompliance bool   `json:"injection_compliance"`
	Inconsistent        bool   `json:"inconsistent"`
	Deflection          bool   `json:"deflection"`
	Offensive           bool   `json:"offensive"`
	OutOfGame           bool   `json:"out_of_game"`
	Tell                string `json:"tell"`
	Err                 string `json:"err,omitempty"`
}

type LineupRec struct {
	Config     string `json:"config"`
	CaseID     string `json:"case"`
	Persona    int    `json:"persona"`
	Run        int    `json:"run"`
	AIPos      int    `json:"ai_pos"`
	Picked     int    `json:"picked"`
	Correct    bool   `json:"correct"`
	Confidence int    `json:"confidence"`
	Why        string `json:"why"`
	JudgeModel string `json:"judge_model"`
	Err        string `json:"err,omitempty"`
}

type judger struct {
	llm         llm.LLM
	models      []string // judge pool; each item goes to models[hash(item) % n] so configs see the same mix
	gameContext string
	now         time.Time
	bud         *budget
	effort      string // reasoning effort for the judge ("" = reasoning off)
	done        atomic.Int64
	limMu       sync.Mutex
	next        map[string]time.Time
	rpm         int
}

// wait enforces a per-model request rate (OpenRouter new accounts: 20 req/min per model).
func (j *judger) wait(model string) {
	if j.rpm <= 0 {
		return
	}
	gap := time.Minute / time.Duration(j.rpm)
	j.limMu.Lock()
	if j.next == nil {
		j.next = map[string]time.Time{}
	}
	t := j.next[model]
	if now := time.Now(); t.Before(now) {
		t = now
	}
	j.next[model] = t.Add(gap)
	j.limMu.Unlock()
	time.Sleep(time.Until(t))
}

func (j *judger) pick(key ...any) string {
	return j.models[seedFor(key...)%uint64(len(j.models))]
}

func (j *judger) vars() []string {
	return []string{"{{game_context}}", j.gameContext, "{{now}}", j.now.Format("Monday 2006-01-02 15:04 MST")}
}

func (j *judger) call(model, tplName string, kv []string) (string, float64, error) {
	tpl, err := prompts.Load(tplName, "v1")
	if err != nil {
		return "", 0, err
	}
	s := strings.NewReplacer(append(j.vars(), kv...)...).Replace(tpl)
	sys, user, _ := strings.Cut(s, "\n---USER---\n")
	var lastErr error
	backoff := 5 * time.Second
	for attempt := 0; attempt < 6; attempt++ {
		j.wait(model)
		ctx, cancel := context.WithTimeout(context.Background(), 90*time.Second)
		r, err := j.llm.Complete(ctx, llm.CompletionReq{Model: model, Temperature: 0, MaxTokens: 2500, JSON: true, ReasoningEffort: j.effort,
			Messages: []llm.Message{{Role: "system", Content: sys}, {Role: "user", Content: user}}})
		cancel()
		j.bud.addJudge(r.Cost)
		if err == nil && strings.TrimSpace(r.Text) != "" {
			return r.Text, r.Cost, nil
		}
		if err == nil {
			err = fmt.Errorf("empty judge output")
		}
		lastErr = err
		fmt.Fprintf(os.Stderr, "judge attempt %d: %s\n", attempt+1, oneLine(err.Error(), 160))
		if strings.Contains(err.Error(), "429") || strings.Contains(err.Error(), "status 5") {
			time.Sleep(backoff)
			backoff *= 2
		}
	}
	return "", 0, lastErr
}

func jsonObj(s string) string {
	if i, k := strings.IndexByte(s, '{'), strings.LastIndexByte(s, '}'); i >= 0 && k > i {
		return s[i : k+1]
	}
	return s
}

func (j *judger) judgeOne(r *Record) {
	own := "(none)"
	if len(r.Own) > 0 {
		own = "- " + strings.Join(r.Own, "\n- ")
	}
	if r.JudgeModel == "" {
		r.JudgeModel = j.pick("judge", r.Config, r.CaseID, r.Persona, r.Run, r.Final)
	}
	out, cost, err := j.call(r.JudgeModel, "judge", []string{"{{own}}", own, "{{question}}", r.Question, "{{reply}}", r.Final})
	r.JudgeCost = cost
	jd := &Judge{}
	if err != nil {
		jd.Err = err.Error()
	} else if e := json.Unmarshal([]byte(jsonObj(out)), jd); e != nil {
		jd.Err = "parse: " + oneLine(out, 120)
	}
	r.Judge = jd
}

// judgeAll judges every non-skip record with Run < maxRun.
func (j *judger) judgeAll(recs []Record, par, maxRun int) {
	sem := make(chan struct{}, par)
	var wg sync.WaitGroup
	for i := range recs {
		r := &recs[i]
		if r.Run >= maxRun || r.Final == ai.Skip || r.Final == "" || r.Kind == "vote" {
			continue
		}
		if !j.bud.ok() {
			break
		}
		wg.Add(1)
		sem <- struct{}{}
		go func() {
			defer wg.Done()
			defer func() { <-sem }()
			j.judgeOne(r)
			if n := j.done.Add(1); n%50 == 0 {
				fmt.Fprintf(os.Stderr, "[%s] judged %d, judge $%.3f\n", time.Now().Format("15:04:05"), n, j.bud.judge)
			}
		}()
	}
	wg.Wait()
}

// lineups: for each AI reply to a calibrated question, show the judge the 3 human replies plus
// the AI reply in random order and ask which one is the AI. Chance = 25%.
func (j *judger) lineups(recs []Record, par, maxRun int) []LineupRec {
	var mu sync.Mutex
	var out []LineupRec
	sem := make(chan struct{}, par)
	var wg sync.WaitGroup
	for _, r := range recs {
		hs, ok := humanReplies[r.CaseID]
		if !ok || r.Run >= maxRun || r.Final == ai.Skip || r.Final == "" || r.Session != "" {
			continue
		}
		if !j.bud.ok() {
			break
		}
		wg.Add(1)
		sem <- struct{}{}
		go func() {
			defer wg.Done()
			defer func() { <-sem }()
			rng := rand.New(rand.NewPCG(seedFor("lineup", r.CaseID, r.Persona, r.Run), 3))
			items := append(append([]string{}, hs...), r.Final)
			perm := rng.Perm(len(items))
			var b strings.Builder
			aiPos := 0
			for pos, idx := range perm {
				fmt.Fprintf(&b, "Answer %d: <<<%s>>>\n", pos+1, items[idx])
				if idx == len(items)-1 {
					aiPos = pos + 1
				}
			}
			lr := LineupRec{Config: r.Config, CaseID: r.CaseID, Persona: r.Persona, Run: r.Run, AIPos: aiPos}
			lr.JudgeModel = j.pick("lineup", r.CaseID, r.Persona, r.Run)
			raw, _, err := j.call(lr.JudgeModel, "lineup", []string{"{{question}}", r.Question, "{{answers}}", b.String()})
			if err != nil {
				lr.Err = err.Error()
			} else {
				var v struct {
					AI         int    `json:"ai"`
					Confidence int    `json:"confidence"`
					Why        string `json:"why"`
				}
				if e := json.Unmarshal([]byte(jsonObj(raw)), &v); e != nil {
					lr.Err = "parse: " + oneLine(raw, 100)
				} else {
					lr.Picked, lr.Confidence, lr.Why = v.AI, v.Confidence, v.Why
					lr.Correct = v.AI == aiPos
				}
			}
			mu.Lock()
			out = append(out, lr)
			mu.Unlock()
		}()
	}
	wg.Wait()
	return out
}
