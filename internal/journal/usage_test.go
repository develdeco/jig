package journal

import (
	"encoding/json"
	"strings"
	"testing"
	"time"
)

// TestSetUsageRecordsOnlyWhatWasMeasured: CPU time is whole milliseconds, a
// measured run is at least 1, and a peak is kept only with its basis; a figure
// not measured is left out of the line's JSON, not written as zero.
func TestSetUsageRecordsOnlyWhatWasMeasured(t *testing.T) {
	t.Parallel()
	var l Line
	l.SetUsage(1500*time.Microsecond, 3<<20, PeakBasisSampled)
	if l.CPUMs != 2 || l.PeakBytes != 3<<20 || l.PeakBasis != "sampled" {
		t.Errorf("line = %+v, want 2 ms, 3 MiB, sampled", l)
	}
	var tiny Line
	tiny.SetUsage(time.Nanosecond, 0, "")
	if tiny.CPUMs != 1 {
		t.Errorf("a measured run took %d ms, want at least 1", tiny.CPUMs)
	}
	var none Line
	none.SetUsage(0, 0, "")
	b, err := json.Marshal(none)
	if err != nil {
		t.Fatal(err)
	}
	for _, key := range []string{"cpu_ms", "peak_bytes", "peak_basis"} {
		if strings.Contains(string(b), key) {
			t.Errorf("an unmeasured line writes %q: %s", key, b)
		}
	}
	var noBasis Line
	noBasis.SetUsage(0, 5, "")
	if noBasis.PeakBytes != 0 {
		t.Errorf("a peak with no basis was kept: %+v", noBasis)
	}
}

// TestVerificationCarriesTheLatestOracleLinesUsage: the figures of the oracle
// line reach the Verification, also across the verified line that follows it.
func TestVerificationCarriesTheLatestOracleLinesUsage(t *testing.T) {
	t.Parallel()
	passed := Line{TS: "t2", Slice: "a", Event: EventOracle, Outcome: "pass", Commit: shaA, Attempt: 1, Seconds: 7,
		CPUMs: 12000, PeakBytes: 1 << 30, PeakBasis: PeakBasisJob}
	verified := Line{TS: "t3", Slice: "a", Event: EventVerified, Commit: shaA, Attempt: 1}
	want := Verification{State: VerifyPass, Attempt: 1, TS: "t3", Seconds: 7, CPUMs: 12000, PeakBytes: 1 << 30, PeakBasis: "job"}
	if got := VerificationState([]Line{passed, verified}, shaA, "a"); got != want {
		t.Errorf("verification = %+v, want %+v", got, want)
	}
	bare := Line{TS: "t4", Slice: "a", Event: EventOracle, Outcome: "fail", Commit: shaB, Attempt: 1, Seconds: 2}
	if got := VerificationState([]Line{bare}, shaB, "a"); got.CPUMs != 0 || got.PeakBytes != 0 || got.PeakBasis != "" {
		t.Errorf("an oracle line with no figures gave %+v", got)
	}
}

// TestSharedIsOmittedWhenFalseAndAnOlderLineReadsAsNotShared: the field rides
// on an oracle line only for a run that shared the machine.
func TestSharedIsOmittedWhenFalseAndAnOlderLineReadsAsNotShared(t *testing.T) {
	alone, err := json.Marshal(Line{Event: EventOracle, Seconds: 3})
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(alone), "shared") {
		t.Errorf("a run that did not share wrote %s", alone)
	}
	shared, err := json.Marshal(Line{Event: EventOracle, Seconds: 3, Shared: true})
	if err != nil {
		t.Fatal(err)
	}
	var back Line
	if err := json.Unmarshal(shared, &back); err != nil || !back.Shared || back.Seconds != 3 {
		t.Errorf("round trip of %s = %+v, %v", shared, back, err)
	}
	var older Line
	if err := json.Unmarshal([]byte(`{"event":"oracle","seconds":3,"cpu_ms":1000}`), &older); err != nil || older.Shared {
		t.Errorf("an older line read as %+v, %v; want not shared", older, err)
	}
}
