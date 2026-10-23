package gamma

import "testing"

func TestShout(t *testing.T) {
	if got := Shout("hi"); got != "HI!" {
		t.Fatalf("Shout(hi) = %q, want HI!", got)
	}
}
