// Package staircase selects a model rung for a build attempt, and picks a
// model disjoint from ones already used by other builders in a ticket.
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
	// FailedAttempts is how many earlier attempts of this slice ended
	// without green.
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

// Disjoint picks a model not already used by any builder this ticket: the
// first cheap-to-dear rung not in used, or the dearest rung if all are used.
func Disjoint(cfg Config, used []string) string {
	n := len(cfg.Rungs)
	if n == 0 {
		return ""
	}
	usedSet := make(map[string]bool, len(used))
	for _, u := range used {
		usedSet[u] = true
	}
	for _, r := range cfg.Rungs {
		if !usedSet[r] {
			return r
		}
	}
	return cfg.Rungs[n-1]
}
