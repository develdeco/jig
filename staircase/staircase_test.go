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
		{"diff lines over threshold", Signals{DiffLines: 401}, "b"},
		{"diff files over threshold", Signals{DiffFiles: 11}, "b"},
		{"both volume signals", Signals{DiffLines: 401, DiffFiles: 11}, "b"},
		{"invariant", Signals{Invariant: true}, "c"},
		{"invariant and volume", Signals{Invariant: true, DiffLines: 401}, "c"},
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
		for _, s := range []Signals{{}, {DiffLines: 401}, {Invariant: true}, {DiffFiles: 11, Invariant: true}} {
			if got := Select(single, s); got != "only" {
				t.Fatalf("Select(single, %+v) = %q, want %q", s, got, "only")
			}
		}
	})
}

func TestDisjoint(t *testing.T) {
	cfg := Config{Rungs: []string{"a", "b", "c"}}

	cases := []struct {
		name string
		used []string
		want string
	}{
		{"one used", []string{"a"}, "b"},
		{"all used", []string{"a", "b", "c"}, "c"},
		{"none used", []string{}, "a"},
		{"middle used", []string{"b"}, "a"},
	}

	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got := Disjoint(cfg, c.used)
			if got != c.want {
				t.Fatalf("Disjoint(cfg, %v) = %q, want %q", c.used, got, c.want)
			}
		})
	}
}

func TestDefault(t *testing.T) {
	got := Default().Rungs
	want := []string{"claude-haiku-4-5", "claude-sonnet-5", "claude-opus-5"}
	if len(got) != len(want) {
		t.Fatalf("Default().Rungs = %v, want %v", got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("Default().Rungs = %v, want %v", got, want)
		}
	}
}
