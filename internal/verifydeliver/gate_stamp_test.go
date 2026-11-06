package verifydeliver

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/develdeco/jig/internal/fixture"
	"gopkg.in/yaml.v3"
)

// TestGateRoundNamesTheBuild: a gate round's journal lines and its
// report.yaml carry the build stamp of the jig that closed the round.
func TestGateRoundNamesTheBuild(t *testing.T) {
	t.Parallel()
	fx := fixture.Generate(t, fixture.Opts{Home: t.TempDir()})
	driveBuild(t, fx, "rung-a")
	d := newDeps(t, fx)
	d.Oracle = nil
	report, err := Gate(d, NewFakeGateSource(fx.ScenarioDir), GateOpts{Ticket: fx.Ticket})
	if err != nil {
		t.Fatalf("Gate: %v", err)
	}

	dir := d.Store.TicketDir(fx.Ticket)
	data, err := os.ReadFile(filepath.Join(dir, "journal.ndjson"))
	if err != nil {
		t.Fatal(err)
	}
	gateLines := 0
	for _, row := range bytes.Split(bytes.TrimSpace(data), []byte("\n")) {
		var raw map[string]any
		if err := json.Unmarshal(row, &raw); err != nil {
			t.Fatal(err)
		}
		if event, _ := raw["event"].(string); strings.HasPrefix(event, "gate-") {
			gateLines++
			if got, _ := raw["jig"].(string); got == "" {
				t.Errorf("%s line has no jig field: %s", event, row)
			}
		}
	}
	if gateLines == 0 {
		t.Fatal("journal has no gate lines")
	}

	reportData, err := os.ReadFile(filepath.Join(gateRoundDir(d.Store, fx.Ticket, report.Round), "report.yaml"))
	if err != nil {
		t.Fatal(err)
	}
	var raw map[string]any
	if err := yaml.Unmarshal(reportData, &raw); err != nil {
		t.Fatal(err)
	}
	if got, _ := raw["jig"].(string); got == "" {
		t.Errorf("report.yaml has no jig field:\n%s", reportData)
	}
}
