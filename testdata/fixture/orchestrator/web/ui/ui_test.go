package ui

import "testing"

func TestLabel(t *testing.T) {
	if got := Label(405); got != "$4.05" {
		t.Fatalf("Label(405) = %q, want $4.05", got)
	}
}
