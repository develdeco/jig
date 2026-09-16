package beta

import "testing"

func TestGreet(t *testing.T) {
	if got := Greet("World"); got != "Hello, World." {
		t.Fatalf("Greet(%q) = %q, want %q", "World", got, "Hello, World.")
	}
}
