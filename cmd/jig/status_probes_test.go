package main

import (
	"strings"
	"testing"
)

// TestStatusProbes: the rounds table counts each round's ledger, and the probes,
// probe_skips and unprobed tables list it as the rounds recorded it: every
// probe of every round in the order taken, every skip, and the latest recording
// round's unprobed hunks. A round no reviewer ran has dashes, a round that did
// not record its unprobed hunks has a dash for them, and a ticket with no probe
// prints none of the tables.
func TestStatusProbes(t *testing.T) {
	t.Parallel()
	fx := newStoreOnlyFixture(t)
	st := openStore(t, fx)
	if got := statusOf(t, fx, st); strings.Contains(got, "probes[") || strings.Contains(got, "probe_skips[") || strings.Contains(got, "unprobed[") {
		t.Errorf("a ticket with no round has ledger tables:\n%s", got)
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
	round3 := func(findings string) {
		writeRound(t, fx.StoreDir, fx.Ticket, 3, roundFiles{
			report:   "round: 3\nverdict: clean\nreviewed_sha:\n  fixture-repo: " + sha40("7") + "\nindependent: true\nreviewer:\n  session: s-rev\n",
			findings: "scope: delta\nreviewed_paths: [a.go]\nfindings: []\n" + findings,
		})
	}
	round3("probes:\n  - {seam: greeting, file: web/a.go, line: 4, mutation: again, command: go test ./web, outcome: caught, blob: b1}\nunprobed: []\n")
	writeRound(t, fx.StoreDir, fx.Ticket, 4, roundFiles{
		report:   "round: 4\nverdict: clean\nreviewed_sha:\n  fixture-repo: " + sha40("8") + "\nindependent: true\nreviewer:\n  session: s-rev\n",
		findings: "scope: delta\nreviewed_paths: [a.go]\nfindings: []\n",
	})

	out := statusOf(t, fx, st)
	rounds := block(out, "rounds[")
	for i, tail := range []string{",-,-,-,-", ",2,1,2,1", ",1,0,0,0", ",0,0,0,-"} {
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
	skips := block(out, "probe_skips[")
	wantSkips := []string{
		"probe_skips[2]{round,file,range,why}:",
		"  2,web/a_test.go,whole file,a test file",
		"  2,gen.go,3-8,generated",
	}
	if strings.Join(skips, "\n") != strings.Join(wantSkips, "\n") {
		t.Errorf("probe_skips block:\n%s\nwant\n%s", strings.Join(skips, "\n"), strings.Join(wantSkips, "\n"))
	}
	// Round 3 recorded that it left none and is the latest to record, so the
	// earlier round's hunk is not the table.
	if strings.Contains(out, "\nunprobed[") {
		t.Errorf("an unprobed table is printed though the latest recording round left none:\n%s", out)
	}

	// Without round 3's empty list, round 2's hunk is the latest recorded.
	round3("")
	got := block(statusOf(t, fx, st), "unprobed[")
	want := []string{"unprobed[1]{round,file,from,to}:", "  2,web/c.go,1,5"}
	if strings.Join(got, "\n") != strings.Join(want, "\n") {
		t.Errorf("unprobed block:\n%s\nwant\n%s", strings.Join(got, "\n"), strings.Join(want, "\n"))
	}
}
