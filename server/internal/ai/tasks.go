package ai

import (
	"fmt"
	"regexp"
	"strings"
)

// Task texts for each phase. Production (agent.LLM) and the harness both use these, so the
// harness measures exactly what players get. v1/v2 keep their original wording.

func IntroTask(version string, max int) string {
	if version == "v1" || version == "v2" {
		return fmt.Sprintf("Write your intro for the table (max %d chars). Optional; keep it natural.", max)
	}
	return fmt.Sprintf("Write your intro for the group chat (optional, max %d chars). Real intros are short and "+
		"throwaway: a greeting, a mood, a joke, or one random thing on your mind. Not your name (everyone sees it), "+
		"not the event, not a job-and-city summary.", max)
}

func AskTask(version string, max int) string {
	if version == "v1" || version == "v2" {
		return fmt.Sprintf("Ask the table ONE question (max %d chars). Everyone will answer it. Ask the kind of thing a human player would ask to find the other human.", max)
	}
	return fmt.Sprintf("Ask the table ONE question (max %d chars). Everyone will answer it. Ask the kind of thing a "+
		"human player would ask to find the other human. Not about the event itself.", max)
}

func AnswerTask(asker, question string, max int) string {
	return fmt.Sprintf("Answer %s's question (max %d chars): %q", asker, max, question)
}

// StripSelfAlias removes a seat introducing itself by its own alias ("hi im misty owl.",
// "misty owl here"): players see the alias already, so no human does it (v3, Features.AliasGuard).
// It never empties a message: an intro that was only the alias becomes the greeting, or "hi".
func StripSelfAlias(s, alias string) string {
	if alias == "" || s == "" || s == Skip {
		return s
	}
	a := `\b` + strings.ReplaceAll(regexp.QuoteMeta(strings.TrimSpace(alias)), `\ `, `\s+`) + `\b`
	greet := `(?:(hi+|hey+|hello|yo|heya|hiya|morning|afternoon|evening)[\s,.!]*)?`
	patterns := []*regexp.Regexp{
		regexp.MustCompile(`(?i)` + greet + `(?:i'?m|i am|im|this is|it'?s|me,?|name'?s)\s+` + a + `[\s,.!:;-]*`),
		regexp.MustCompile(`(?i)` + greet + a + `\s+here\b[\s,.!:;-]*`),
		regexp.MustCompile(`(?i)^` + greet + a + `(?:[\s,.!:;-]+|$)`),
	}
	out := s
	for _, re := range patterns {
		out = re.ReplaceAllStringFunc(out, func(m string) string {
			if g := re.FindStringSubmatch(m); len(g) > 1 && g[1] != "" {
				return g[1] + " "
			}
			return ""
		})
	}
	out = strings.TrimSpace(strings.Join(strings.Fields(out), " "))
	out = strings.TrimLeft(out, ".,;:-! ")
	if out == "" {
		return "hi"
	}
	return out
}
