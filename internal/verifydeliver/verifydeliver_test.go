package verifydeliver

import (
	"errors"
	"fmt"
	"testing"

	"github.com/develdeco/jig/internal/axi"
)

// TestFailureCode pins failureCode's own branches directly, as a pure
// function, rather than by paying for another full fixture and gate
// rounds: Publish's own push_error_axi_code_only subtest already shows
// Gate and Publish call failureCode rather than err.Error() (its stubbed
// axi.Error's Msg and Code deliberately differ, so a fallback to the raw
// message would fail that subtest too), leaving failureCode's remaining
// branches - a wrapped *axi.Error, one with no code, and a plain error -
// to pin here instead.
func TestFailureCode(t *testing.T) {
	plain := errors.New("exit status 1: /host/secret/abs/path: permission denied")
	coded := &axi.Error{Msg: "push rejected: /host/secret/abs/path", Code: "PUBLISH_PUSH_REJECTED"}
	uncoded := &axi.Error{Msg: "no code here"}
	wrapped := fmt.Errorf("verifydeliver: publish: guarded push: %w", coded)

	cases := []struct {
		name string
		err  error
		want string
	}{
		{"axi error with a code", coded, "PUBLISH_PUSH_REJECTED"},
		{"axi error wrapped by fmt.Errorf", wrapped, "PUBLISH_PUSH_REJECTED"},
		{"axi error with an empty code", uncoded, "INTERNAL"},
		{"plain error", plain, "INTERNAL"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if got := failureCode(c.err); got != c.want {
				t.Fatalf("failureCode(%v) = %q, want %q", c.err, got, c.want)
			}
		})
	}
}
