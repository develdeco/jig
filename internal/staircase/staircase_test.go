package staircase

import "testing"

func TestTransitions(t *testing.T) {
	cfg := Config{Rungs: []string{"a", "b", "c"}}

	cases := []struct {
		name string
		s    Signals
		want string
	}{
		{"no signals", Signals{}, "a"},
		{"one failed attempt", Signals{FailedAttempts: 1}, "b"},
		{"two failed attempts", Signals{FailedAttempts: 2}, "c"},
		{"more failures than rungs", Signals{FailedAttempts: 5}, "c"},
		{"invariant", Signals{Invariant: true}, "c"},
		{"invariant and a failure", Signals{Invariant: true, FailedAttempts: 1}, "c"},
	}

	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got := Select(cfg, c.s)
			if got != c.want {
				t.Fatalf("Select(%+v) = %q, want %q", c.s, got, c.want)
			}
		})
	}

	t.Run("single-rung config clamps", func(t *testing.T) {
		single := Config{Rungs: []string{"only"}}
		for _, s := range []Signals{{}, {FailedAttempts: 2}, {Invariant: true}, {FailedAttempts: 1, Invariant: true}} {
			if got := Select(single, s); got != "only" {
				t.Fatalf("Select(single, %+v) = %q, want %q", s, got, "only")
			}
		}
	})
}

func TestDearest(t *testing.T) {
	if got := Dearest(Config{Rungs: []string{"a", "b", "c"}}); got != "c" {
		t.Errorf("Dearest(a, b, c) = %q, want c", got)
	}
	if got := Dearest(Config{}); got != "" {
		t.Errorf("Dearest(no rungs) = %q, want empty", got)
	}
}

// TestDefault: builds open on Sonnet and climb to Opus; Haiku is used only
// where a project lists it in project.yaml's staircase (ADR 0019).
func TestDefault(t *testing.T) {
	got := Default().Rungs
	want := []string{"claude-sonnet-5", "claude-opus-5"}
	if len(got) != len(want) {
		t.Fatalf("Default().Rungs = %v, want %v", got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("Default().Rungs = %v, want %v", got, want)
		}
	}
}
