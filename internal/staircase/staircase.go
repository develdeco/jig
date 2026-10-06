// Package staircase selects a model rung for a build attempt, and names the
// dearest rung, the gate reviewer's model.
// Model axis only: stateless selection over a cheap-to-dear rung list.
package staircase

// Config lists the model rungs, cheapest first.
type Config struct {
	Rungs []string
}

// Default returns the built-in rung list, cheap to dear. It opens on Sonnet:
// a project that wants a cheaper opening rung lists it first in
// project.yaml's staircase (ADR 0019).
func Default() Config {
	return Config{Rungs: []string{"claude-sonnet-5", "claude-opus-5"}}
}

// Signals are the measurements Select climbs or floors the rung on.
type Signals struct {
	// FailedAttempts is how many earlier attempts of this slice failed at
	// the work (journal.FailedAttempts): not questions, flawed briefs or
	// blocked environments, which are not the builder failing.
	FailedAttempts int
	// Invariant reports whether any file changed in the lease matches a
	// declared invariant.
	Invariant bool
}

// Select picks a rung for cfg given s. Selection opens on the first rung and
// climbs one rung per failed attempt of the slice; an invariant match floors
// to the dearest rung, overriding everything else. The result is always
// clamped to cfg's bounds.
func Select(cfg Config, s Signals) string {
	n := len(cfg.Rungs)
	if n == 0 {
		return ""
	}
	idx := s.FailedAttempts
	if s.Invariant {
		idx = n - 1
	}
	if idx > n-1 {
		idx = n - 1
	}
	if idx < 0 {
		idx = 0
	}
	return cfg.Rungs[idx]
}

// Dearest is the staircase's dearest rung, the gate reviewer's model on
// every round: its independence comes from a fresh, read-only session, not
// from a model the builders did not use (ADR 0023). "" when cfg has no
// rungs.
func Dearest(cfg Config) string {
	if len(cfg.Rungs) == 0 {
		return ""
	}
	return cfg.Rungs[len(cfg.Rungs)-1]
}
