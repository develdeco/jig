package verifydeliver

import (
	"fmt"
	"strings"
	"testing"
)

// headTrailer commits jigYAML as jig.yaml (none when empty) in a tiny repo and
// returns what reviewedTrailer takes from that head with fallback, and the
// warnings it raised.
func headTrailer(t *testing.T, jigYAML, fallback string) (string, []string) {
	t.Helper()
	dir, _ := tinyRepo(t)
	head := run(t, dir, "rev-parse", "HEAD")
	if jigYAML != "" {
		head = writeAndCommit(t, dir, "jig.yaml", jigYAML, "add the marker")
	}
	var warns []string
	got := reviewedTrailer(dir, head, fallback, func(format string, args ...any) {
		warns = append(warns, fmt.Sprintf(format, args...))
	})
	return got, warns
}

// TestReviewedTrailerIsTheHeadsWhenConfigHasNone: the machine's marker sets no
// trailer, the reviewed head's jig.yaml does, and the render takes the head's.
func TestReviewedTrailerIsTheHeadsWhenConfigHasNone(t *testing.T) {
	t.Parallel()
	got, warns := headTrailer(t, "jig: \"1\"\nkind: project\npublish:\n  trailer: from the head\n", "")
	if got != "from the head" || len(warns) != 0 {
		t.Errorf("trailer = %q, warnings %q; want the head's and none", got, warns)
	}
}

// TestReviewedTrailerOfTheHeadWinsOverConfigs: both set one; the head's wins.
func TestReviewedTrailerOfTheHeadWinsOverConfigs(t *testing.T) {
	t.Parallel()
	got, _ := headTrailer(t, "kind: project\npublish:\n  trailer: from the head\n", "from the clone")
	if got != "from the head" {
		t.Errorf("trailer = %q, want the head's", got)
	}
}

// TestReviewedTrailerFallsBackWithoutAHeadMarker: a head without a jig.yaml
// takes Config's trailer.
func TestReviewedTrailerFallsBackWithoutAHeadMarker(t *testing.T) {
	t.Parallel()
	got, warns := headTrailer(t, "", "from the clone")
	if got != "from the clone" || len(warns) != 0 {
		t.Errorf("trailer = %q, warnings %q; want Config's and none", got, warns)
	}
}

// TestReviewedTrailerFallsBackOnAMemberMarker: a member marker at the head
// takes Config's trailer, whatever the member marker says.
func TestReviewedTrailerFallsBackOnAMemberMarker(t *testing.T) {
	t.Parallel()
	got, _ := headTrailer(t, "kind: member\npublish:\n  trailer: from the member\n", "from the clone")
	if got != "from the clone" {
		t.Errorf("trailer = %q, want Config's", got)
	}
}

// TestReviewedTrailerFallsBackOnAnUnparsableHeadMarker: a head jig.yaml that is
// not YAML takes Config's trailer and warns once.
func TestReviewedTrailerFallsBackOnAnUnparsableHeadMarker(t *testing.T) {
	t.Parallel()
	got, warns := headTrailer(t, "kind: [unclosed\n  : nope\n", "from the clone")
	if got != "from the clone" || len(warns) != 1 || !strings.Contains(warns[0], "does not parse") {
		t.Errorf("trailer = %q, warnings %q; want Config's and one parse warning", got, warns)
	}
}
