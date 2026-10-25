package lib

import "testing"

func TestDollars(t *testing.T) {
	if got := Dollars(450); got != 4 {
		t.Fatalf("Dollars(450) = %d, want 4", got)
	}
}
