package verifydeliver

import (
	"fmt"
	"strings"
	"testing"
)

// TestRefusalReasonIsOneBoundedLine: a backend error can carry a whole stderr
// tail; the record keeps one line of it.
func TestRefusalReasonIsOneBoundedLine(t *testing.T) {
	t.Parallel()
	got := refusalReason(fmt.Errorf("the pick session failed:\n  exit 1\r\n\t%s", strings.Repeat("x", 1000)))
	if strings.ContainsAny(got, "\r\n\t") || strings.Contains(got, "  ") {
		t.Errorf("reason %q keeps line breaks or runs of whitespace", got)
	}
	if !strings.HasPrefix(got, "the pick session failed: exit 1 xxx") {
		t.Errorf("reason = %q", got[:40])
	}
	if want := refusalReasonCap + len("..."); len([]rune(got)) != want {
		t.Errorf("reason is %d runes, want it capped at %d", len([]rune(got)), want)
	}
	short := refusalReason(fmt.Errorf("short"))
	if short != "short" {
		t.Errorf("a short reason = %q, want it unchanged", short)
	}
}
