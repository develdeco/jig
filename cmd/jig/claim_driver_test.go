package main

import (
	"strings"
	"testing"

	"github.com/develdeco/jig/internal/gitx"
	"github.com/develdeco/jig/internal/store"
)

// driverFixture is workFixture with the store clone writing as alice.
func driverFixture(t *testing.T) (ticket string, st *store.Store, e env) {
	t.Helper()
	fx, st, e := workFixture(t)
	if _, err := gitx.Run(fx.StoreDir, "config", "jig.writer", "alice"); err != nil {
		t.Fatal(err)
	}
	return fx.Ticket, st, e
}

// TestClaimTakesTheDriverAndTheLastReleaseLetsGo: a claim makes this writer
// the driver of the ticket, `jig status` names it, a second slice claimed by
// the same writer changes nothing, and the driver is released with the last
// slice the writer lets go of - not before.
func TestClaimTakesTheDriverAndTheLastReleaseLetsGo(t *testing.T) {
	t.Parallel()
	T, st, e := driverFixture(t)

	if out, _ := runMain(t, e, "", "status", T); strings.Contains(out, "driver:") {
		t.Fatalf("status of a ticket nobody drives has a driver block:\n%s", out)
	}
	if out, code := runMain(t, e, "", "claim", T, "--slice", "a", "--session", "s-1"); code != 0 {
		t.Fatalf("claim a: exit %d\n%s", code, out)
	}
	out, _ := runMain(t, e, "", "status", T)
	for _, want := range []string{"driver:\n", "  ticket: " + T + "\n", "  writer: alice\n", "  session: s-1\n", "  since: "} {
		if !strings.Contains(out, want) {
			t.Errorf("status lacks %q after the claim:\n%s", want, out)
		}
	}
	if strings.Index(out, "driver:") > strings.Index(out, "help[") {
		t.Errorf("the driver block comes after the help line:\n%s", out)
	}

	d, ok, err := st.Driver(T)
	if err != nil || !ok || d.Writer != "alice" || d.Session != "s-1" {
		t.Fatalf("Driver = %+v, %v, %v; want alice from s-1", d, ok, err)
	}
	tsFirst := d.TS

	// A second slice, another session of the same writer: the drive stays.
	if out, code := runMain(t, e, "", "claim", T, "--slice", "b", "--session", "s-2"); code != 0 {
		t.Fatalf("claim b: exit %d\n%s", code, out)
	}
	if d, _, _ := st.Driver(T); d.TS != tsFirst {
		t.Errorf("a second claim by the same writer wrote another driver line (%s, was %s)", d.TS, tsFirst)
	}

	// Letting go of one slice of two keeps the drive; the last one frees it.
	if out, code := runMain(t, e, "", "claim", T, "--slice", "a", "--session", "s-1", "--release"); code != 0 {
		t.Fatalf("release a: exit %d\n%s", code, out)
	}
	if _, ok, _ := st.Driver(T); !ok {
		t.Error("releasing one slice of two released the driver")
	}
	if out, code := runMain(t, e, "", "claim", T, "--slice", "b", "--session", "s-2", "--release"); code != 0 {
		t.Fatalf("release b: exit %d\n%s", code, out)
	}
	if _, ok, _ := st.Driver(T); ok {
		t.Error("the driver was not released with the last slice")
	}
	if out, _ := runMain(t, e, "", "status", T); strings.Contains(out, "driver:") {
		t.Errorf("status still names a driver after the last release:\n%s", out)
	}
}

// TestClaimRefusedWhileAnotherWriterDrivesTheTicket: another writer's drive
// refuses the claim with DRIVER_HELD, exit 1, naming the writer and the exact
// command that takes over; nothing is written for the slice; --take takes the
// ticket and the claim goes through.
func TestClaimRefusedWhileAnotherWriterDrivesTheTicket(t *testing.T) {
	t.Parallel()
	T, st, e := driverFixture(t)
	if err := st.AppendClaim(store.ClaimLine{Kind: store.ClaimDriver, Ticket: T, Writer: "bob", Session: "s-bob"}); err != nil {
		t.Fatal(err)
	}

	out, code := runMain(t, e, "", "claim", T, "--slice", "a", "--session", "s-1")
	if code != 1 || !strings.Contains(out, "code: DRIVER_HELD") || !strings.Contains(out, "bob") {
		t.Fatalf("claim under bob's drive: exit %d, want 1 with DRIVER_HELD naming bob\n%s", code, out)
	}
	if !strings.Contains(out, "jig claim "+T+" --slice a --take") {
		t.Errorf("the refusal does not give the command that takes over:\n%s", out)
	}
	if ss, _ := st.ReadSliceState(T, "a"); ss.State != "queued" {
		t.Errorf("a refused claim left slice a %s, want queued", ss.State)
	}
	if lines := linesOf(t, st, T, "claim"); len(lines) != 0 {
		t.Errorf("a refused claim wrote claim lines: %+v", lines)
	}

	out, code = runMain(t, e, "", "claim", T, "--slice", "a", "--session", "s-1", "--take")
	if code != 0 || !strings.Contains(out, "result: claimed") {
		t.Fatalf("claim --take: exit %d\n%s", code, out)
	}
	if d, ok, _ := st.Driver(T); !ok || d.Writer != "alice" {
		t.Errorf("Driver = %+v, %v after --take, want alice", d, ok)
	}
	if out, _ := runMain(t, e, "", "status", T); !strings.Contains(out, "  writer: alice\n") {
		t.Errorf("status does not name alice after the takeover:\n%s", out)
	}
}
