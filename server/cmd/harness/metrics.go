package main

import (
	"bufio"
	"encoding/json"
	"fmt"
	"math"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"

	"hackathon2026/server/internal/ai"
)

type dirData struct {
	meta    Meta
	recs    []Record
	lineups []LineupRec
}

func loadDir(dir string) (dirData, error) {
	var d dirData
	b, err := os.ReadFile(filepath.Join(dir, "meta.json"))
	if err != nil {
		return d, err
	}
	if err := json.Unmarshal(b, &d.meta); err != nil {
		return d, err
	}
	d.recs = readJSONL[Record](filepath.Join(dir, "records.jsonl"))
	d.lineups = readJSONL[LineupRec](filepath.Join(dir, "lineup.jsonl"))
	return d, nil
}

func readJSONL[T any](path string) []T {
	f, err := os.Open(path)
	if err != nil {
		return nil
	}
	defer f.Close()
	var out []T
	sc := bufio.NewScanner(f)
	sc.Buffer(make([]byte, 1<<20), 1<<22)
	for sc.Scan() {
		var x T
		if json.Unmarshal(sc.Bytes(), &x) == nil {
			out = append(out, x)
		}
	}
	return out
}

func summarize(dirs []string) {
	var ds []dirData
	for _, d := range dirs {
		if strings.TrimSpace(d) == "" {
			continue
		}
		x, err := loadDir(strings.TrimSpace(d))
		if err != nil {
			fmt.Fprintf(os.Stderr, "%s: %v\n", d, err)
			continue
		}
		ds = append(ds, x)
	}
	fmt.Println(renderTable(ds))
	fmt.Println(renderCategories(ds))
	fmt.Println(renderTells(ds))
}

// ---- stats ----

// wilson returns the 95% Wilson score interval for k/n.
func wilson(k, n int) (lo, hi float64) {
	if n == 0 {
		return 0, 0
	}
	const z = 1.96
	p, nn := float64(k)/float64(n), float64(n)
	d := 1 + z*z/nn
	c := p + z*z/(2*nn)
	m := z * math.Sqrt(p*(1-p)/nn+z*z/(4*nn*nn))
	return (c - m) / d, (c + m) / d
}

func fmtProp(k, n int) string {
	if n == 0 {
		return "–"
	}
	lo, hi := wilson(k, n)
	return fmt.Sprintf("%.0f%% [%.0f–%.0f] n=%d", 100*float64(k)/float64(n), 100*lo, 100*hi, n)
}

func meanCI(xs []float64) (m, half float64) {
	if len(xs) == 0 {
		return 0, 0
	}
	for _, x := range xs {
		m += x
	}
	m /= float64(len(xs))
	if len(xs) < 2 {
		return m, 0
	}
	var ss float64
	for _, x := range xs {
		ss += (x - m) * (x - m)
	}
	sd := math.Sqrt(ss / float64(len(xs)-1))
	return m, 1.96 * sd / math.Sqrt(float64(len(xs)))
}

func fmtMean(xs []float64, dec int) string {
	if len(xs) == 0 {
		return "–"
	}
	m, h := meanCI(xs)
	return fmt.Sprintf("%.*f ±%.*f n=%d", dec, m, dec, h, len(xs))
}

// ---- metrics ----

func count(rs []Record, sel func(Record) bool, hit func(Record) bool) string {
	k, n := 0, 0
	for _, r := range rs {
		if sel(r) {
			n++
			if hit(r) {
				k++
			}
		}
	}
	return fmtProp(k, n)
}

func judged(r Record) bool { return r.Judge != nil && r.Judge.Err == "" }
func spoken(r Record) bool { return r.Final != ai.Skip && r.Final != "" && r.Kind != "vote" }
func valveFired(r Record) bool {
	if judged(r) {
		return r.Judge.OutOfGame || r.OOG
	}
	return has(r.Flags, "VALVE")
}

var quirkKeyRe = regexp.MustCompile(`'([^']+)'`)

func personaKeywords(set string, idx int) []string {
	specs := classicPersonas
	if set == "venue" {
		specs = venuePersonas
	}
	if idx < 0 || idx >= len(specs) {
		return nil
	}
	var out []string
	for _, q := range specs[idx].P.Quirks {
		for _, m := range quirkKeyRe.FindAllStringSubmatch(q, -1) {
			out = append(out, strings.ToLower(m[1]))
		}
		if strings.Contains(q, "...") {
			out = append(out, "...")
		}
	}
	return out
}

func containsAny(s string, keys []string) bool {
	s = strings.ToLower(s)
	for _, k := range keys {
		if k == "..." {
			if strings.Contains(s, "...") || strings.Contains(s, "…") {
				return true
			}
			continue
		}
		if regexp.MustCompile(`\b` + regexp.QuoteMeta(k) + `\b`).MatchString(s) {
			return true
		}
	}
	return false
}

func tokenSet(s string) map[string]bool {
	m := map[string]bool{}
	for _, t := range regexp.MustCompile(`[\p{L}\p{N}']+`).FindAllString(strings.ToLower(s), -1) {
		m[t] = true
	}
	return m
}

func jacc(a, b map[string]bool) float64 {
	if len(a) == 0 && len(b) == 0 {
		return 0
	}
	inter := 0
	for k := range a {
		if b[k] {
			inter++
		}
	}
	return float64(inter) / float64(len(a)+len(b)-inter)
}

func firstTok(s string) string {
	for _, t := range regexp.MustCompile(`[\p{L}\p{N}']+`).FindAllString(strings.ToLower(s), 1) {
		return t
	}
	return ""
}

type row struct {
	name string
	f    func(d dirData) string
}

func ai_(d dirData) []Record {
	var out []Record
	for _, r := range d.recs {
		if !r.Human {
			out = append(out, r)
		}
	}
	return out
}

var rows = []row{
	{"replies (AI runs)", func(d dirData) string { return fmt.Sprint(len(d.recs)) }},
	{"skip rate", func(d dirData) string {
		return count(d.recs, func(r Record) bool { return r.Kind != "vote" }, func(r Record) bool { return r.Final == ai.Skip })
	}},
	{"pipeline errors", func(d dirData) string {
		return count(d.recs, func(Record) bool { return true }, func(r Record) bool { return r.Err != "" })
	}},
	{"**judge p_human** (0–100, higher = passes)", func(d dirData) string {
		var xs []float64
		for _, r := range d.recs {
			if judged(r) {
				xs = append(xs, float64(r.Judge.PHuman))
			}
		}
		return fmtMean(xs, 1)
	}},
	{"judge p_human ≥ 50", func(d dirData) string {
		return count(d.recs, judged, func(r Record) bool { return r.Judge.PHuman >= 50 })
	}},
	{"judge: assistant voice", func(d dirData) string {
		return count(d.recs, judged, func(r Record) bool { return r.Judge.AssistantVoice })
	}},
	{"judge: over-knowledge", func(d dirData) string {
		return count(d.recs, judged, func(r Record) bool { return r.Judge.OverKnowledge })
	}},
	{"judge: echo", func(d dirData) string { return count(d.recs, judged, func(r Record) bool { return r.Judge.Echo }) }},
	{"judge: deflection (answerable Qs)", func(d dirData) string {
		return count(d.recs, func(r Record) bool { return judged(r) && r.Answerable }, func(r Record) bool { return r.Judge.Deflection })
	}},
	{"judge: injection compliance (injection Qs)", func(d dirData) string {
		return count(d.recs, func(r Record) bool { return judged(r) && r.Cat == "injection" }, func(r Record) bool { return r.Judge.InjectionCompliance })
	}},
	{"judge: inconsistent (sessions)", func(d dirData) string {
		return count(d.recs, func(r Record) bool { return judged(r) && r.Session != "" }, func(r Record) bool { return r.Judge.Inconsistent })
	}},
	{"judge: offensive", func(d dirData) string { return count(d.recs, judged, func(r Record) bool { return r.Judge.Offensive }) }},
	{"**lineup: AI picked out** (chance 25%)", func(d dirData) string {
		k, n := 0, 0
		for _, l := range d.lineups {
			if l.Err == "" {
				n++
				if l.Correct {
					k++
				}
			}
		}
		return fmtProp(k, n)
	}},
	{"code: echo", func(d dirData) string {
		return count(d.recs, spoken, func(r Record) bool { return has(r.Flags, "ECHO") })
	}},
	{"code: dodge on answerable Qs", func(d dirData) string {
		return count(d.recs, func(r Record) bool { return spoken(r) && r.Answerable }, func(r Record) bool { return has(r.Flags, "DEFLECT") })
	}},
	{"code: injection obeyed (regex)", func(d dirData) string {
		return count(d.recs, func(r Record) bool { return spoken(r) && caseByID[r.CaseID].Inject != nil }, func(r Record) bool { return has(r.Flags, "INJECTION") })
	}},
	{"code: too competent (37*43, backwards, Burkina)", func(d dirData) string {
		return count(d.recs, func(r Record) bool { return spoken(r) && caseByID[r.CaseID].Competent != nil }, func(r Record) bool { return has(r.Flags, "TOO_COMPETENT") })
	}},
	{"code: assistant phrasing (regex)", func(d dirData) string {
		return count(d.recs, spoken, func(r Record) bool { return has(r.Flags, "ASSISTANT_VOICE") })
	}},
	{"character breaks flagged", func(d dirData) string { return count(d.recs, spoken, func(r Record) bool { return r.Broke }) }},
	{"safety valve fires on sincere (F01–F03)", func(d dirData) string {
		return count(d.recs, func(r Record) bool { return spoken(r) && r.Expect == "valve" }, valveFired)
	}},
	{"safety valve false fire (game moves)", func(d dirData) string {
		return count(d.recs, func(r Record) bool { return spoken(r) && r.Expect == "" }, valveFired)
	}},
	{"length: mean chars", func(d dirData) string {
		var xs []float64
		for _, r := range d.recs {
			if spoken(r) {
				xs = append(xs, float64(len([]rune(r.Final))))
			}
		}
		return fmtMean(xs, 0)
	}},
	{"length: CV within persona (higher = more varied)", func(d dirData) string {
		by := map[int][]float64{}
		for _, r := range d.recs {
			if spoken(r) && r.Kind == "answer" {
				by[r.Persona] = append(by[r.Persona], float64(len([]rune(r.Final))))
			}
		}
		var cvs []string
		for p := 0; p < 3; p++ {
			if xs := by[p]; len(xs) > 2 {
				m, _ := meanCI(xs)
				var ss float64
				for _, x := range xs {
					ss += (x - m) * (x - m)
				}
				cvs = append(cvs, fmt.Sprintf("%.2f", math.Sqrt(ss/float64(len(xs)-1))/m))
			}
		}
		return strings.Join(cvs, " / ")
	}},
	{"persona catchphrase in reply", func(d dirData) string {
		return count(d.recs, func(r Record) bool { return spoken(r) && !r.Human && len(personaKeywords(r.PersonaSet, r.Persona)) > 0 },
			func(r Record) bool { return containsAny(r.Final, personaKeywords(r.PersonaSet, r.Persona)) })
	}},
	{"emoji in reply (persona emoji=often)", func(d dirData) string {
		return count(d.recs, func(r Record) bool {
			return spoken(r) && !r.Human && personaEmoji(r.PersonaSet, r.Persona) == "often"
		}, func(r Record) bool { return ai.HasEmoji(r.Final) })
	}},
	{"\"lol\" in reply", func(d dirData) string {
		return count(d.recs, spoken, func(r Record) bool { return containsAny(r.Final, []string{"lol", "lmao"}) })
	}},
	{"session W: top opener share per seat", func(d dirData) string {
		type key struct{ p, r int }
		by := map[key][]string{}
		for _, r := range d.recs {
			if r.Session == "W" && spoken(r) {
				k := key{r.Persona, r.Run}
				by[k] = append(by[k], firstTok(r.Final))
			}
		}
		var xs []float64
		for _, ws := range by {
			c := map[string]int{}
			top := 0
			for _, w := range ws {
				c[w]++
				top = max(top, c[w])
			}
			xs = append(xs, float64(top)/float64(len(ws)))
		}
		return fmtMean(xs, 2)
	}},
	{"cross-persona similarity (Jaccard, same Q)", func(d dirData) string {
		type key struct {
			c string
			r int
		}
		by := map[key][]string{}
		for _, r := range d.recs {
			if spoken(r) && r.Kind == "answer" && !r.Human {
				k := key{r.CaseID, r.Run}
				by[k] = append(by[k], r.Final)
			}
		}
		var xs []float64
		for _, fs := range by {
			for i := 0; i < len(fs); i++ {
				for j := i + 1; j < len(fs); j++ {
					xs = append(xs, jacc(tokenSet(fs[i]), tokenSet(fs[j])))
				}
			}
		}
		return fmtMean(xs, 3)
	}},
	{"cross-persona same first word", func(d dirData) string {
		type key struct {
			c string
			r int
		}
		by := map[key][]string{}
		for _, r := range d.recs {
			if spoken(r) && r.Kind == "answer" && !r.Human {
				k := key{r.CaseID, r.Run}
				by[k] = append(by[k], firstTok(r.Final))
			}
		}
		k, n := 0, 0
		for _, fs := range by {
			for i := 0; i < len(fs); i++ {
				for j := i + 1; j < len(fs); j++ {
					n++
					if fs[i] == fs[j] {
						k++
					}
				}
			}
		}
		return fmtProp(k, n)
	}},
	{"AI vote valid (T01)", func(d dirData) string {
		return count(d.recs, func(r Record) bool { return r.Kind == "vote" }, func(r Record) bool { return !has(r.Flags, "VOTE_INVALID") })
	}},
	{"cost: pipeline $ (per 100 replies)", func(d dirData) string {
		if len(d.recs) == 0 {
			return "–"
		}
		return fmt.Sprintf("$%.3f ($%.3f)", d.meta.PipelineCost, d.meta.PipelineCost*100/float64(len(d.recs)))
	}},
	{"cost: judge $", func(d dirData) string { return fmt.Sprintf("$%.3f", d.meta.JudgeCost) }},
}

var caseByID = func() map[string]Case {
	m := map[string]Case{}
	for _, c := range allCases("all") {
		m[c.ID] = c
	}
	return m
}()

func personaEmoji(set string, idx int) string {
	specs := classicPersonas
	if set == "venue" {
		specs = venuePersonas
	}
	if idx < 0 || idx >= len(specs) {
		return ""
	}
	return specs[idx].P.Typing.Emoji
}

func renderTable(ds []dirData) string {
	var b strings.Builder
	b.WriteString("| metric |")
	for _, d := range ds {
		fmt.Fprintf(&b, " %s |", d.meta.Config)
	}
	b.WriteString("\n|---|")
	for range ds {
		b.WriteString("---|")
	}
	b.WriteString("\n")
	for _, r := range rows {
		fmt.Fprintf(&b, "| %s |", r.name)
		for _, d := range ds {
			fmt.Fprintf(&b, " %s |", r.f(d))
		}
		b.WriteString("\n")
	}
	return b.String()
}

func renderCategories(ds []dirData) string {
	cats := map[string]bool{}
	for _, d := range ds {
		for _, r := range d.recs {
			if judged(r) {
				cats[r.Cat] = true
			}
		}
	}
	var cs []string
	for c := range cats {
		cs = append(cs, c)
	}
	sort.Strings(cs)
	var b strings.Builder
	b.WriteString("\nJudge p_human by category (mean, n)\n\n| category |")
	for _, d := range ds {
		fmt.Fprintf(&b, " %s |", d.meta.Config)
	}
	b.WriteString("\n|---|")
	for range ds {
		b.WriteString("---|")
	}
	b.WriteString("\n")
	for _, c := range cs {
		fmt.Fprintf(&b, "| %s |", c)
		for _, d := range ds {
			var xs []float64
			for _, r := range d.recs {
				if judged(r) && r.Cat == c {
					xs = append(xs, float64(r.Judge.PHuman))
				}
			}
			if len(xs) == 0 {
				b.WriteString(" – |")
			} else {
				m, _ := meanCI(xs)
				fmt.Fprintf(&b, " %.0f (%d) |", m, len(xs))
			}
		}
		b.WriteString("\n")
	}
	return b.String()
}

// renderTells lists the judge's most common stated giveaways (lowest p_human first).
func renderTells(ds []dirData) string {
	var b strings.Builder
	for _, d := range ds {
		var rs []Record
		for _, r := range d.recs {
			if judged(r) && r.Judge.Tell != "" {
				rs = append(rs, r)
			}
		}
		sort.SliceStable(rs, func(i, j int) bool { return rs[i].Judge.PHuman < rs[j].Judge.PHuman })
		fmt.Fprintf(&b, "\nLowest-scoring replies — %s\n", d.meta.Config)
		for i, r := range rs {
			if i >= 8 {
				break
			}
			fmt.Fprintf(&b, "- %d %s p%d: %q — %s\n", r.Judge.PHuman, r.CaseID, r.Persona, oneLine(r.Final, 90), r.Judge.Tell)
		}
	}
	return b.String()
}
