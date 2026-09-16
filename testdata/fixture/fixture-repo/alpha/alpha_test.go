package alpha

import "testing"

func TestAdd(t *testing.T) {
	if got := Add(2, 3); got != 5 {
		t.Fatalf("Add(2, 3) = %d, want 5", got)
	}
}

func TestClamp(t *testing.T) {
	if got := Clamp(10, 0, 5); got != 5 {
		t.Fatalf("Clamp(10, 0, 5) = %d, want 5", got)
	}
}
