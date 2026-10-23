package spend

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/develdeco/jig/internal/project"
	"github.com/develdeco/jig/internal/store"
)

func newStore(t *testing.T) *store.Store {
	t.Helper()
	return &store.Store{Root: t.TempDir()}
}

// TestBudgetRecordRoundTrip: a ticket with no record reads as unrecorded, a
// record reads back, and a record that sets no limit is still a record - the
// ticket was given no budget on purpose.
func TestBudgetRecordRoundTrip(t *testing.T) {
	t.Parallel()
	st := newStore(t)
	if _, recorded, err := ReadBudget(st, "T-1"); err != nil || recorded {
		t.Fatalf("a ticket with no record: recorded = %v, err = %v", recorded, err)
	}
	want := project.Budget{Tokens: 5_000_000, USD: 12.5}
	if err := WriteBudget(st, "T-1", want); err != nil {
		t.Fatal(err)
	}
	if got, recorded, err := ReadBudget(st, "T-1"); err != nil || !recorded || got != want {
		t.Fatalf("read back %+v, recorded = %v, err = %v, want %+v", got, recorded, err, want)
	}
	data, _ := os.ReadFile(filepath.Join(st.Root, "tickets", "T-1", BudgetFile))
	if string(data) != "tokens: 5000000\nusd: 12.5\n" {
		t.Errorf("budget.yaml = %q, want the two keys", data)
	}
	if err := WriteBudget(st, "T-1", project.Budget{}); err != nil {
		t.Fatal(err)
	}
	if got, recorded, err := ReadBudget(st, "T-1"); err != nil || !recorded || Limited(got) {
		t.Errorf("an empty record: %+v, recorded = %v, err = %v, want recorded and limitless", got, recorded, err)
	}
	for _, bad := range []project.Budget{{Tokens: -1}, {USD: -0.5}} {
		if err := WriteBudget(st, "T-1", bad); err == nil {
			t.Errorf("WriteBudget(%+v) = nil, want a refusal", bad)
		}
	}
}

// TestCheckStates: near at 80% of a limit, over past one, whichever limit
// is the closer; a limit that is not set is not held against.
func TestCheckStates(t *testing.T) {
	t.Parallel()
	spent := func(tokens int64, usd float64) Totals { return Totals{Input: tokens, USD: usd} }
	b := project.Budget{Tokens: 1000, USD: 10}
	for _, c := range []struct {
		name  string
		t     Totals
		b     project.Budget
		want  State
		ratio float64
	}{
		{"no budget", spent(1e9, 1e9), project.Budget{}, StateNone, 0},
		{"well under", spent(100, 1), b, StateOK, 0.1},
		{"near on tokens", spent(800, 1), b, StateNear, 0.8},
		{"near on dollars", spent(10, 9), b, StateNear, 0.9},
		{"exactly at the limit is not over", spent(1000, 0), b, StateNear, 1},
		{"over on tokens", spent(1001, 0), b, StateOver, 1.001},
		{"over on dollars", spent(0, 10.5), b, StateOver, 1.05},
		{"tokens only", spent(500, 1e6), project.Budget{Tokens: 1000}, StateOK, 0.5},
		{"dollars only", spent(1e12, 5), project.Budget{USD: 10}, StateOK, 0.5},
	} {
		got := Check(c.t, c.b)
		if got.State != c.want || !near(got.Ratio, c.ratio) {
			t.Errorf("%s: Check = %+v, want state %s at ratio %v", c.name, got, c.want, c.ratio)
		}
	}
}

// TestProjectDefaultComesFromTheNearestMarker: the marker nearest the first
// directory that has one gives the default, and a marker with no budget gives
// none, even when a marker above it has one.
func TestProjectDefaultComesFromTheNearestMarker(t *testing.T) {
	t.Parallel()
	root := t.TempDir()
	member := filepath.Join(root, "member")
	other := filepath.Join(t.TempDir(), "none")
	for _, dir := range []string{member, other} {
		if err := os.MkdirAll(dir, 0o755); err != nil {
			t.Fatal(err)
		}
	}
	if err := project.WriteMarker(root, project.Marker{Project: "p", Budget: &project.Budget{Tokens: 7, USD: 1}}); err != nil {
		t.Fatal(err)
	}
	if b, ok := ProjectDefault("", other, member); !ok || b != (project.Budget{Tokens: 7, USD: 1}) {
		t.Errorf("default = %+v, %v, want the marker above member", b, ok)
	}
	if err := project.WriteMarker(member, project.Marker{Project: "p", Kind: project.KindMember}); err != nil {
		t.Fatal(err)
	}
	if b, ok := ProjectDefault(member); ok {
		t.Errorf("default = %+v, want none: the nearer marker sets no budget", b)
	}
	if _, ok := ProjectDefault(other, ""); ok {
		t.Error("a directory in no project has a default budget")
	}
}
