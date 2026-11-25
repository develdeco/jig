package main

import (
	"strings"
	"testing"
)

// TestStatusProbes: the rounds table counts each round's probes, and the probes
// table lists them as the rounds recorded them, every probe of every round in
// the order taken. The skipped and unprobed keys an older jig wrote are ignored:
// they add no column and no table. A round no reviewer ran has dashes, and a
// ticket with no probe prints no probes table.
func TestStatusProbes(t *testing.T) {
	t.Parallel()
	fx := newStoreOnlyFixture(t)
	st := openStore(t, fx)
	if got := statusOf(t, fx, st); strings.Contains(got, "probes[") {
		t.Errorf("a ticket with no round has a probes table:\n%s", got)
	}

	writeRound(t, fx.StoreDir, fx.Ticket, 1, roundFiles{report: "round: 1\nverdict: fix-slices\ntarget_sha:\n  fixture-repo: " + sha40("5") + "\n"})
	writeRound(t, fx.StoreDir, fx.Ticket, 2, roundFiles{
		report: "round: 2\nverdict: fix-slices\nreviewed_sha:\n  fixture-repo: " + sha40("6") + "\nindependent: true\nreviewer:\n  session: s-rev\n",
		findings: "scope: full\nreviewed_paths: [a.go]\nfindings: []\nprobes:\n" +
			"  - {seam: greeting, file: web/a.go, line: 4, mutation: \"drop, the check\", command: go test ./web, outcome: survived, blob: b1}\n" +
			"  - {seam: parse, file: web/a.go, line: 9, mutation: flip it, command: go test ./web, outcome: caught, blob: b1}\n" +
			"skipped:\n  - {file: web/a_test.go, why: a test file, blob: b2}\n  - {file: gen.go, from: 3, to: 8, why: generated, blob: b3}\n" +
			"unprobed:\n  - {file: web/c.go, from: 1, to: 5}\n",
	})
	writeRound(t, fx.StoreDir, fx.Ticket, 3, roundFiles{
		report: "round: 3\nverdict: clean\nreviewed_sha:\n  fixture-repo: " + sha40("7") + "\nindependent: true\nreviewer:\n  session: s-rev\n",
		findings: "scope: delta\nreviewed_paths: [a.go]\nfindings: []\n" +
			"probes:\n  - {seam: greeting, file: web/a.go, line: 4, mutation: again, command: go test ./web, outcome: caught, blob: b1}\nunprobed: []\n",
	})
	writeRound(t, fx.StoreDir, fx.Ticket, 4, roundFiles{
		report:   "round: 4\nverdict: clean\nreviewed_sha:\n  fixture-repo: " + sha40("8") + "\nindependent: true\nreviewer:\n  session: s-rev\n",
		findings: "scope: delta\nreviewed_paths: [a.go]\nfindings: []\n",
	})

	out := statusOf(t, fx, st)
	rounds := block(out, "rounds[")
	if !strings.HasSuffix(rounds[0], ",files,kept_fixed,probes,survived}:") {
		t.Errorf("rounds header %q, want it to end in the probe counts", rounds[0])
	}
	for i, tail := range []string{",-,0,-,-", ",1,0,2,1", ",1,0,1,0", ",1,0,0,0"} {
		if row := rounds[i+1]; !strings.HasSuffix(row, tail) {
			t.Errorf("round %d row %q does not end in %q", i+1, row, tail)
		}
	}
	probes := block(out, "probes[")
	wantProbes := []string{
		"probes[3]{round,outcome,file:line,seam,mutation,command}:",
		`  2,survived,"web/a.go:4",greeting,"drop, the check",go test ./web`,
		`  2,caught,"web/a.go:9",parse,flip it,go test ./web`,
		`  3,caught,"web/a.go:4",greeting,again,go test ./web`,
	}
	if strings.Join(probes, "\n") != strings.Join(wantProbes, "\n") {
		t.Errorf("probes block:\n%s\nwant\n%s", strings.Join(probes, "\n"), strings.Join(wantProbes, "\n"))
	}
	for _, unwanted := range []string{"skip", "unprobed", "a test file", "web/c.go"} {
		if strings.Contains(out, unwanted) {
			t.Errorf("status shows %q of an older round's keys:\n%s", unwanted, out)
		}
	}
}
