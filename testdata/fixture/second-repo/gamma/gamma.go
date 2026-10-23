// Package gamma is the second repo's one workspace.
package gamma

// Shout returns s upper-cased with an exclamation mark.
func Shout(s string) string {
	b := []byte(s)
	for i, c := range b {
		if c >= 'a' && c <= 'z' {
			b[i] = c - 'a' + 'A'
		}
	}
	return string(b) + "!"
}
