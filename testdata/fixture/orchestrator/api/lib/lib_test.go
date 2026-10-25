package lib

import "testing"

func TestCents(t *testing.T) {
	if got := Cents(3); got != 300 {
		t.Fatalf("Cents(3) = %d, want 300", got)
	}
}
