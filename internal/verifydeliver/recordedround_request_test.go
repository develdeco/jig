package verifydeliver

import (
	"os"
	"testing"
)

// gateReviewRequest opens a minimal clean round and returns the review.json that
// round's reviewer was actually handed, rather than anything the round reports,
// which cannot catch the request losing a field on the way to the reviewer. It
// also pins the intent field's raw key names (requireIntentWireShape), which
// decoding into ReviewRequest cannot see.
func gateReviewRequest(t *testing.T, d Deps, ticket string, opts GateOpts) ReviewRequest {
	t.Helper()
	var got ReviewRequest
	opts.Ticket = ticket
	recordedRound(t, d, opts, func(p Prepared) ReviewResult {
		data, err := os.ReadFile(p.RequestPath)
		if err != nil {
			t.Fatalf("read review.json: %v", err)
		}
		requireIntentWireShape(t, data)
		got = p.Request
		return cleanReport(p)
	})
	return got
}
