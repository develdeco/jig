package verify

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	"github.com/develdeco/jig/internal/fixture"
)

// TestRunNamesTheBuildOnItsOracleLines: the oracle and verified lines a
// verification writes carry the build stamp of the jig that ran it.
func TestRunNamesTheBuildOnItsOracleLines(t *testing.T) {
	t.Parallel()
	r := newRig(t, fixture.Opts{})
	sha := r.fixClamp()
	if _, err := Run(r.deps, r.request("a", sha)); err != nil {
		t.Fatalf("Run: %v", err)
	}
	data, err := os.ReadFile(filepath.Join(r.st.TicketDir(r.fx.Ticket), "journal.ndjson"))
	if err != nil {
		t.Fatal(err)
	}
	seen := map[string]bool{}
	for _, row := range bytes.Split(bytes.TrimSpace(data), []byte("\n")) {
		var raw map[string]any
		if err := json.Unmarshal(row, &raw); err != nil {
			t.Fatal(err)
		}
		event, _ := raw["event"].(string)
		if event != "oracle" && event != "verified" {
			continue
		}
		seen[event] = true
		if got, _ := raw["jig"].(string); got == "" {
			t.Errorf("%s line has no jig field: %s", event, row)
		}
	}
	if !seen["oracle"] || !seen["verified"] {
		t.Fatalf("journal lacks an oracle or verified line: %v", seen)
	}
}
