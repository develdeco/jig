package tui

import "testing"

func TestModelNameIsHowPeopleCallTheModel(t *testing.T) {
	t.Parallel()
	for id, want := range map[string]string{
		"claude-sonnet-5":           "Sonnet 5",
		"claude-opus-5-5":           "Opus 5.5",
		"claude-opus-5-5[1m]":       "Opus 5.5",
		"claude-haiku-4-5-2025100":  "Haiku 4.5.2025100",
		"claude-haiku-4-5-20251001": "Haiku 4.5",
		"sonnet":                    "Sonnet",
		"gpt-5":                     "Gpt 5",
		"":                          "",
	} {
		if got := modelName(id); got != want {
			t.Errorf("modelName(%q) = %q, want %q", id, got, want)
		}
	}
}

func TestPersonNameDropsTheEmail(t *testing.T) {
	t.Parallel()
	if got := personName("Ada Lovelace <ada@example.invalid>"); got != "Ada Lovelace" {
		t.Errorf("personName = %q", got)
	}
	if got := personName("Ada"); got != "Ada" {
		t.Errorf("personName = %q", got)
	}
}
