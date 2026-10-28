package tui

import "testing"

// TestAGoalOfSeveralLinesReadsAsSentences: a fix slice's goal is a heading,
// then a finding's title and its reason on lines of their own; the card shows
// them as one paragraph, and a title that ends with no stop gets one, so it
// does not run into the reason.
func TestAGoalOfSeveralLinesReadsAsSentences(t *testing.T) {
	t.Parallel()
	goal := "Fix these gate findings:\n\ncart/cart_test.go:9 No test pins the rounding\nThe repro case cannot tell rounding from truncation.\nRisk (medium): money"
	want := "cart/cart_test.go:9 No test pins the rounding. The repro case cannot tell rounding from truncation. Risk (medium): money"
	if got := goalText(goal); got != want {
		t.Errorf("goalText = %q, want %q", got, want)
	}
	if got := goalLine(goal); got != "cart/cart_test.go:9 No test pins the rounding" {
		t.Errorf("goalLine = %q, want the finding's title alone", got)
	}
}
