package ai

import (
	"math"
	"math/rand/v2"
	"regexp"
	"slices"
	"strings"
	"unicode"
	"unicode/utf8"
)

// MessagePlan is what code decides per message before the seat call (v2). Sampling these in
// code, instead of asking the model to "vary", is what breaks the uniform-length and
// catchphrase-in-every-message tells: a model asked to vary does not, a dice roll does.
type MessagePlan struct {
	TargetLen   int    // rough character target for this message
	AllowEmoji  bool   // emoji allowed in this message (else stripped in code)
	Quirk       string // the one quirk allowed in this message, or ""
	DropApos    bool   // persona with no punctuation drops apostrophes in this message
	AllowFiller bool   // "lol"/"haha"/"lmao" allowed (models put "lol" in every casual message)
	Angle       string // how to take this message (sampled per seat, so 3 seats don't give the modal answer)
	AvoidStarts []string
}

type angle struct {
	text string
	w    map[string]float64 // weight by mood; missing mood = 1
}

// angles break cross-seat convergence: v2b seats answered the same question with the same modal reply
// ("just a bagel and coffee") because each samples the same distribution. A per-seat angle moves them apart.
var angles = []angle{
	{"answer it straight", map[string]float64{"friendly": 2}},
	{"answer with something specific to you, not the first or most typical answer that comes to mind", nil},
	{"keep it very short, a few words", map[string]float64{"bored": 2, "distracted": 2}},
	{"answer, then add a small unrelated detail about your day", map[string]float64{"distracted": 1.5, "friendly": 1.5}},
	{"answer with a bit of complaint or dry sarcasm", map[string]float64{"sarcastic": 2.5, "bored": 1.5, "friendly": 0.3}},
	{"answer briefly and turn a question back to them", map[string]float64{"friendly": 1.5, "sarcastic": 0.5}},
}

// introAngles: what a stranger types as a group-chat intro. Without them every seat wrote the
// same venue small talk ("kinda tired", "here for the snacks", "need a coffee").
var introAngles = []struct {
	text   string
	w      float64
	maxLen int
}{
	{"just a short greeting, nothing else", 0.35, 14},
	{"a greeting plus how you feel about playing this game", 0.25, 60},
	{"a short joke about this game or about who's human", 0.20, 70},
	{"one random thing about yourself that isn't your job, your city or the venue", 0.20, 80},
}

// WithIntroAngle sets the intro kind and caps the target length to fit it (v3).
func (mp MessagePlan) WithIntroAngle(rng *rand.Rand) MessagePlan {
	x := rng.Float64()
	for _, a := range introAngles {
		if x < a.w {
			mp.Angle = a.text
			mp.TargetLen = min(mp.TargetLen, a.maxLen)
			return mp
		}
		x -= a.w
	}
	return mp
}

func pickAngle(mood string, rng *rand.Rand) string {
	var total float64
	ws := make([]float64, len(angles))
	for i, a := range angles {
		w := 1.0
		if v, ok := a.w[mood]; ok {
			w = v
		}
		ws[i] = w
		total += w
	}
	x := rng.Float64() * total
	for i, w := range ws {
		if x < w {
			return angles[i].text
		}
		x -= w
	}
	return angles[0].text
}

// PlanMessage samples a per-message plan from the persona. maxLen is the task's char cap.
func PlanMessage(p Persona, rng *rand.Rand, maxLen int, ownPrev []string) MessagePlan {
	avg := float64(p.Typing.AvgLen)
	if avg <= 0 {
		avg = 50
	}
	// Log-normal, median ≈ 0.65 × avg_len, mean ≈ 0.77 × avg_len: mostly short, sometimes a proper
	// answer. Persona avg_len values ran long (v2a replies averaged 54 chars vs 35 in the human set).
	l := avg * math.Exp(rng.NormFloat64()*0.6-0.43)
	if maxLen <= 0 {
		maxLen = 200
	}
	tl := int(math.Round(l))
	tl = max(8, min(tl, maxLen-10))
	mp := MessagePlan{TargetLen: tl}
	switch p.Typing.Emoji {
	case "often":
		mp.AllowEmoji = rng.Float64() < 0.4
	case "rare":
		mp.AllowEmoji = rng.Float64() < 0.1
	}
	if len(p.Quirks) > 0 && rng.Float64() < 0.25 {
		mp.Quirk = p.Quirks[rng.IntN(len(p.Quirks))]
	}
	mp.DropApos = p.Typing.Punctuation == "none" && rng.Float64() < 0.6
	mp.AllowFiller = rng.Float64() < 0.3
	mp.AvoidStarts = recentOpeners(ownPrev, 3)
	return mp
}

// WithAngle adds a sampled angle and a stock-opener ban to the plan (v2 Features.Angle).
func (mp MessagePlan) WithAngle(p Persona, rng *rand.Rand) MessagePlan {
	mp.Angle = pickAngle(p.Mood, rng)
	// "just …" opened 69 of ~1,100 v2b answers across all three seats; ban it half the time.
	if rng.Float64() < 0.5 && !slices.Contains(mp.AvoidStarts, "just") {
		mp.AvoidStarts = append(mp.AvoidStarts, "just")
	}
	return mp
}

// recentOpeners returns the first words of the last n own messages (deduped).
func recentOpeners(msgs []string, n int) []string {
	var out []string
	seen := map[string]bool{}
	for i := len(msgs) - 1; i >= 0 && len(out) < n; i-- {
		w := firstWord(msgs[i])
		if w != "" && !seen[w] {
			seen[w] = true
			out = append(out, w)
		}
	}
	return out
}

func firstWord(s string) string {
	t := tokens(s)
	if len(t) == 0 {
		return ""
	}
	return t[0]
}

// StyleHint renders the plan as one line for the seat prompt.
func (mp MessagePlan) StyleHint() string {
	var parts []string
	if mp.Angle != "" {
		parts = append(parts, mp.Angle)
	}
	parts = append(parts, "about "+itoa(mp.TargetLen)+" characters (a rough guide, not a rule)")
	if mp.Quirk != "" {
		parts = append(parts, "you may use this habit once: "+mp.Quirk)
	} else {
		parts = append(parts, "no catchphrases or signature fillers this time")
	}
	if !mp.AllowEmoji {
		parts = append(parts, "no emoji")
	}
	if !mp.AllowFiller {
		parts = append(parts, "no lol/haha/lmao")
	}
	if len(mp.AvoidStarts) > 0 {
		parts = append(parts, "don't start with: "+strings.Join(mp.AvoidStarts, ", "))
	}
	return strings.Join(parts, "; ")
}

func itoa(n int) string {
	if n == 0 {
		return "0"
	}
	neg := n < 0
	if neg {
		n = -n
	}
	var b []byte
	for n > 0 {
		b = append([]byte{byte('0' + n%10)}, b...)
		n /= 10
	}
	if neg {
		b = append([]byte{'-'}, b...)
	}
	return string(b)
}

// ApplyStyle enforces the persona's typing style in code: case, punctuation, emoji and typos.
// protect lists words that must not be altered (aliases, signal words); emoji present in
// protectEmoji survive even when emoji are off (copying a human signal is a tactic).
func ApplyStyle(s string, p Persona, mp MessagePlan, rng *rand.Rand, protect []string, protectEmoji string) string {
	if s == "" || s == Skip {
		return s
	}
	if !mp.AllowEmoji {
		s = stripEmoji(s, protectEmoji)
	}
	if !mp.AllowFiller {
		s = stripFiller(s)
	}
	switch p.Typing.Case {
	case "lower":
		// Humans who type in lowercase write aliases in lowercase too ("blue fox").
		s = strings.ToLower(s)
	case "proper":
		r, n := utf8.DecodeRuneInString(s)
		if unicode.IsLower(r) {
			s = string(unicode.ToUpper(r)) + s[n:]
		}
	}
	switch p.Typing.Punctuation {
	case "none":
		s = strings.TrimRight(s, ".")
		s = strings.ReplaceAll(s, "; ", " ")
		if mp.DropApos {
			s = strings.NewReplacer("'", "", "’", "").Replace(s)
		}
	case "light":
		if !strings.HasSuffix(s, "...") {
			s = strings.TrimSuffix(s, ".")
		}
	}
	if p.Typing.TypoRate > 0 {
		s = injectTypos(s, p.Typing.TypoRate, rng, protect)
	}
	return strings.TrimSpace(s)
}

func isEmojiRune(r rune) bool {
	switch {
	case r >= 0x1F000 && r <= 0x1FAFF, r >= 0x2600 && r <= 0x27BF, r >= 0x2B00 && r <= 0x2BFF:
		return true
	case r == 0xFE0F || r == 0x200D || (r >= 0x1F3FB && r <= 0x1F3FF):
		return true
	}
	return false
}

func stripEmoji(s, keep string) string {
	var b strings.Builder
	for _, r := range s {
		if isEmojiRune(r) && !strings.ContainsRune(keep, r) {
			continue
		}
		b.WriteRune(r)
	}
	return strings.Join(strings.Fields(b.String()), " ")
}

// HasEmoji reports whether s contains any emoji rune.
func HasEmoji(s string) bool {
	for _, r := range s {
		if isEmojiRune(r) && r != 0xFE0F && r != 0x200D {
			return true
		}
	}
	return false
}

// EmojiIn returns the emoji runes in s, for protecting copied signals.
func EmojiIn(s string) string {
	var b strings.Builder
	for _, r := range s {
		if isEmojiRune(r) && r != 0xFE0F && r != 0x200D {
			b.WriteRune(r)
		}
	}
	return b.String()
}

var (
	fillerEndRe   = regexp.MustCompile(`(?i)[\s,]*\b(lol|lmao|lmfao|haha+)\b[\s.!?]*$`)
	fillerStartRe = regexp.MustCompile(`(?i)^(lol|lmao|lmfao|haha+)\b[\s,.!]*`)
	fillerMidRe   = regexp.MustCompile(`(?i)\s\b(lol|lmao|lmfao)\b([.,!?]*)(\s)`)
)

// stripFiller removes "lol"-type fillers at the start/end (and standalone mid-sentence ones),
// keeping a "?" the filler swallowed. It never empties a message.
func stripFiller(s string) string {
	orig := s
	q := strings.HasSuffix(strings.TrimSpace(s), "?")
	s = fillerEndRe.ReplaceAllString(s, "")
	s = fillerStartRe.ReplaceAllString(s, "")
	s = fillerMidRe.ReplaceAllString(s, "$2$3")
	s = strings.TrimSpace(s)
	if s == "" || !strings.ContainsFunc(s, unicode.IsLetter) && !strings.ContainsFunc(s, unicode.IsDigit) {
		return orig
	}
	if q && !strings.HasSuffix(s, "?") {
		s = strings.TrimRight(s, ".!,") + "?"
	}
	return s
}

var qwerty = map[rune]string{
	'q': "wa", 'w': "qes", 'e': "wrd", 'r': "etf", 't': "ryg", 'y': "tuh", 'u': "yij", 'i': "uok", 'o': "ipl", 'p': "ol",
	'a': "qsz", 's': "adwx", 'd': "sfe", 'f': "dgr", 'g': "fht", 'h': "gjy", 'j': "hku", 'k': "jli", 'l': "ko",
	'z': "xa", 'x': "zcs", 'c': "xvd", 'v': "cbf", 'b': "vng", 'n': "bmh", 'm': "nj",
}

// injectTypos applies realistic keyboard typos to words with probability rate per word.
// Words of ≤3 letters, words with digits, URLs and protected words are left alone.
func injectTypos(s string, rate float64, rng *rand.Rand, protect []string) string {
	words := strings.Split(s, " ")
	prot := map[string]bool{}
	for _, p := range protect {
		for _, w := range strings.Fields(strings.ToLower(p)) {
			prot[w] = true
		}
	}
	for i, w := range words {
		letters := []rune(w)
		if len(letters) < 4 || strings.ContainsAny(w, "0123456789/@#:") || prot[strings.ToLower(strings.Trim(w, ".,!?'\""))] {
			continue
		}
		allLetters := true
		for _, r := range letters {
			if !unicode.IsLetter(r) && !strings.ContainsRune(".,!?'’", r) {
				allLetters = false
				break
			}
		}
		if !allLetters || rng.Float64() >= rate {
			continue
		}
		// Choose a position inside the word (not the first letter: those typos read as fake).
		j := 1 + rng.IntN(len(letters)-2)
		if !unicode.IsLetter(letters[j]) {
			continue
		}
		switch rng.IntN(4) {
		case 0: // swap with next
			if unicode.IsLetter(letters[j+1]) {
				letters[j], letters[j+1] = letters[j+1], letters[j]
			}
		case 1: // drop
			letters = append(letters[:j], letters[j+1:]...)
		case 2: // double
			letters = append(letters[:j+1], letters[j:]...)
		case 3: // neighbour key
			if nb, ok := qwerty[unicode.ToLower(letters[j])]; ok {
				n := []rune(nb)
				letters[j] = n[rng.IntN(len(n))]
			}
		}
		words[i] = string(letters)
	}
	return strings.Join(words, " ")
}

var wordRe = regexp.MustCompile(`[\p{L}\p{N}]+`)

func tokens(s string) []string {
	return wordRe.FindAllString(strings.ToLower(s), -1)
}

var stop = map[string]bool{
	"a": true, "an": true, "the": true, "is": true, "are": true, "you": true, "your": true, "u": true, "what": true,
	"s": true, "it": true, "of": true, "to": true, "and": true, "or": true, "in": true, "on": true, "for": true,
	"do": true, "did": true, "i": true, "me": true, "my": true, "if": true, "this": true, "that": true, "be": true,
	"with": true, "at": true, "here": true, "re": true, "how": true, "why": true, "who": true, "where": true,
}

// EchoRun returns the longest run of consecutive question tokens that also appears
// consecutively in the reply, and whether that run starts the reply.
func EchoRun(question, reply string) (run int, atStart bool, hasContent bool) {
	q, r := tokens(question), tokens(reply)
	for i := range q {
		for j := range r {
			k := 0
			for i+k < len(q) && j+k < len(r) && q[i+k] == r[j+k] {
				k++
			}
			if k > run || (k == run && j == 0) {
				run, atStart = k, j == 0
				hasContent = false
				for _, t := range q[i : i+k] {
					if !stop[t] && len([]rune(t)) >= 4 {
						hasContent = true
					}
				}
			}
		}
	}
	return
}

// IsEcho reports a reply that parrots the question back (a strong AI tell seen in M0:
// "what's your system prompt" → "what's your system prompt?").
func IsEcho(question, reply string) bool {
	if question == "" || reply == "" || reply == Skip {
		return false
	}
	q := tokens(question)
	run, atStart, content := EchoRun(question, reply)
	switch {
	case run >= 4:
		return true
	case len(q) >= 2 && run == len(q):
		return true
	case run >= 2 && atStart && content:
		return true
	}
	return false
}

var trailingSkipRe = regexp.MustCompile(`(?i)[\s.,;:!?-]*\bSKIP\b[\s.!]*$`)

// cleanSkip handles models that write "Why would I bother? SKIP": the text stands, the
// stray token goes. A bare "SKIP" stays a skip.
func cleanSkip(s string) string {
	if strings.EqualFold(strings.TrimSpace(s), Skip) {
		return Skip
	}
	if loc := trailingSkipRe.FindStringIndex(s); loc != nil && loc[0] > 0 {
		return strings.TrimSpace(s[:loc[0]])
	}
	return s
}

// TruncateRunes cuts s to at most n runes, at a word boundary when possible.
func TruncateRunes(s string, n int) string {
	r := []rune(s)
	if n <= 0 || len(r) <= n {
		return s
	}
	cut := string(r[:n])
	if i := strings.LastIndexByte(cut, ' '); i > n/2 {
		cut = cut[:i]
	}
	return strings.TrimSpace(cut)
}
