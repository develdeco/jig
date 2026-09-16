package board

import (
	"errors"
	"strings"
	"testing"

	"github.com/develdeco/jig/axi"
)

// TestDeferredImplementsBoard is a compile-time-shaped check that Deferred
// satisfies Board, exercised at runtime to also confirm its error shape.
func TestDeferredImplementsBoard(t *testing.T) {
	var b Board = Deferred{}
	err := b.OpenStructuralRound("kind", nil)
	if err == nil {
		t.Fatal("expected an error from the v0.1 stub")
	}
	var ae *axi.Error
	if !errors.As(err, &ae) {
		t.Fatalf("expected *axi.Error, got %T: %v", err, err)
	}
	if ae.Code != "NOT_IMPLEMENTED" {
		t.Fatalf("Code = %q, want NOT_IMPLEMENTED", ae.Code)
	}
	if !strings.Contains(ae.Msg, "v0.2") {
		t.Fatalf("Msg = %q, want it to mention v0.2", ae.Msg)
	}
}
