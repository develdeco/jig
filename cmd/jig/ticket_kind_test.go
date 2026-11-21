package main

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/develdeco/jig/internal/store"
)

// TestTicketKindNewRecordsItAndRefusesOthers: jig ticket new mints a task by
// default, any of the three kinds with --kind, and refuses another value
// before minting.
func TestTicketKindNewRecordsItAndRefusesOthers(t *testing.T) {
	t.Parallel()
	e := testEnv(t.TempDir())
	clone := filepath.Join(t.TempDir(), "clone")
	newTestOriginClone(t, clone)

	mint := func(args ...string) (int, string) {
		var buf bytes.Buffer
		code := run(e, append([]string{"ticket", "new", "--store", clone}, args...), &buf, strings.NewReader(""))
		return code, buf.String()
	}
	if code, out := mint("--title", "a"); code != 0 {
		t.Fatalf("default: exit %d\n%s", code, out)
	}
	if code, out := mint("--title", "b", "--kind", "multi-task"); code != 0 {
		t.Fatalf("multi-task: exit %d\n%s", code, out)
	}
	if code, out := mint("--title", "c", "--kind", "issue"); code != 0 {
		t.Fatalf("issue: exit %d\n%s", code, out)
	}
	code, out := mint("--title", "d", "--kind", "epic")
	if code == 0 || !strings.Contains(out, "must be issue, task or multi-task") {
		t.Fatalf("kind epic: exit %d, want a refusal\n%s", code, out)
	}

	st, err := store.Open(clone)
	if err != nil {
		t.Fatal(err)
	}
	for id, want := range map[string]string{"DEMO-1": store.KindTask, "DEMO-2": store.KindMultiTask, "DEMO-3": store.KindIssue} {
		got, err := st.ReadTicket(id)
		if err != nil {
			t.Fatal(err)
		}
		if got.KindOrDefault() != want {
			t.Errorf("%s kind = %q, want %q", id, got.KindOrDefault(), want)
		}
	}
	if _, err := os.Stat(st.TicketDir("DEMO-4")); err == nil {
		t.Error("a refused kind minted DEMO-4")
	}
}

// TestTicketKindValidateParent: jig validate reports a parent that is not a
// multi-task, one that is not a ticket, and one on a ticket that is not a task.
func TestTicketKindValidateParent(t *testing.T) {
	t.Parallel()
	st := &store.Store{Root: t.TempDir()}
	for id, rec := range map[string]store.Ticket{
		"T-1": {Kind: store.KindMultiTask},
		"T-2": {Parent: "T-1"},
		"T-3": {Parent: "T-2"},
		"T-4": {Parent: "T-404"},
		"T-5": {Kind: store.KindIssue, Parent: "T-1"},
		"T-6": {},
	} {
		mustMkdirTicket(t, st, id)
		if err := st.CreateTicketRecord(id, rec); err != nil {
			t.Fatal(err)
		}
	}
	cases := []struct{ id, want string }{
		{"T-1", ""},
		{"T-2", ""},
		{"T-6", ""},
		{"T-3", `parent "T-2" is a task, not a multi-task`},
		{"T-4", `parent "T-404" is not a ticket of the store`},
		{"T-5", `on a ticket of kind issue; only a task has a parent`},
	}
	for _, tc := range cases {
		got := validateTicketParent(st, tc.id)
		if tc.want == "" {
			if len(got) != 0 {
				t.Errorf("%s: %v, want none", tc.id, got)
			}
			continue
		}
		if len(got) != 1 || !strings.Contains(got[0], tc.want) {
			t.Errorf("%s: %v, want one problem containing %q", tc.id, got, tc.want)
		}
	}
}
