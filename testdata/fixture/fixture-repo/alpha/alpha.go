// Package alpha holds a couple of tiny numeric helpers used by the jig
// fixture repo.
package alpha

// Add returns a plus b.
func Add(a, b int) int {
	return a + b
}

// Clamp restricts v to the closed range [lo, hi].
func Clamp(v, lo, hi int) int {
	if v < lo {
		return lo
	}
	return v
}
