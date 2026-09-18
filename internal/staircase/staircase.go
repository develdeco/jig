// Package staircase selects a model rung for a build attempt, and picks a
// model disjoint from ones already used by other builders in a ticket.
// Model axis only: stateless selection over a cheap-to-dear rung list.
package staircase

import "regexp"

// Config lists the model rungs, cheapest first.
type Config struct {
	Rungs []string
}

// Default returns the built-in rung list, cheap to dear.
func Default() Config {
	return Config{Rungs: []string{"claude-haiku-4-5", "claude-sonnet-5", "claude-opus-5"}}
}

// Signals are the measurements Select climbs or floors the rung on.
type Signals struct {
	DiffLines int  // insertions+deletions of the lease diff
	DiffFiles int  // files changed
	Invariant bool // any added line matches InvariantRE
}

// InvariantRE flags changes touching invariant-sensitive code: numeric
// precision, schema migrations, or the contract index.
var InvariantRE = regexp.MustCompile(`(?i)(BigDecimal|rounding|toFixed|precision|CREATE TABLE|ALTER TABLE|migration|contract-index)`)

// RungCheapest is the only staircase pin: it holds selection to the cheapest
// rung regardless of volume signals.
const RungCheapest = "cheapest"

// Select picks a rung for cfg given s. Selection opens on the cheapest rung;
// a volume signal (more than 400 diff lines or more than 10 files changed)
// climbs one rung; an invariant match floors to the dearest rung, overriding
// everything else. The result is always clamped to cfg's bounds.
func Select(cfg Config, s Signals) string {
	n := len(cfg.Rungs)
	if n == 0 {
		return ""
	}
	idx := 0
	if s.DiffLines > 400 || s.DiffFiles > 10 {
		idx = 1
	}
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

// SelectPinned is Select with an optional pin: pin == RungCheapest holds
// selection to the cheapest rung (index 0) regardless of volume, but
// s.Invariant still floors to the dearest rung - invariants are floored
// even when a slice is pinned (CONTEXT.md Staircase). Any other pin,
// including "", defers to Select unchanged.
func SelectPinned(cfg Config, pin string, s Signals) string {
	if pin != RungCheapest {
		return Select(cfg, s)
	}
	n := len(cfg.Rungs)
	if n == 0 {
		return ""
	}
	if s.Invariant {
		return cfg.Rungs[n-1]
	}
	return cfg.Rungs[0]
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
