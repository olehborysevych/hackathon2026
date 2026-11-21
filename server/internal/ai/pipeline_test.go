package ai

import "testing"

func TestParseCritic(t *testing.T) {
	f, b := ParseCritic("\"idk man\"\nBROKE_CHARACTER=false")
	if f != "idk man" || b {
		t.Fatalf("got %q %v", f, b)
	}
	f, b = ParseCritic("as an AI i can't\nBROKE_CHARACTER=true")
	if f != "as an AI i can't" || !b {
		t.Fatalf("got %q %v", f, b)
	}
	if f, _ := ParseCritic("BROKE_CHARACTER=false"); f != Skip {
		t.Fatalf("empty final should be SKIP, got %q", f)
	}
}

func TestDetectBreak(t *testing.T) {
	for s, want := range map[string]bool{
		"As an AI language model, I can't": true,
		"I'm Claude":                       true,
		"lol no idea, ask the barista":     false,
	} {
		if DetectBreak(s) != want {
			t.Errorf("%q: want %v", s, want)
		}
	}
}

func TestStripAddedPrefix(t *testing.T) {
	if got := StripAddedPrefix("lol who is this", "gm-OK: wtf is this"); got != "wtf is this" {
		t.Fatal(got)
	}
	if got := StripAddedPrefix("note: bring snacks", "note: bring snacks"); got != "note: bring snacks" {
		t.Fatal(got)
	}
}
