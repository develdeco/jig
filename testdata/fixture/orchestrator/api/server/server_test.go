package server

import "testing"

func TestTotal(t *testing.T) {
	if got := Total(150, 250); got != 400 {
		t.Fatalf("Total(150, 250) = %d, want 400", got)
	}
}
