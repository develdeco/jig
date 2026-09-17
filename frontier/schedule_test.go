package frontier

import (
	"reflect"
	"testing"

	"github.com/develdeco/jig/store"
)

func TestScheduleTwoReposConcurrent(t *testing.T) {
	slices := []store.Slice{
		{ID: "a", Workspace: "ws1"},
		{ID: "b", Workspace: "ws2"},
	}
	wsRepo := map[string]string{"ws1": "repo1", "ws2": "repo2"}

	got := Schedule(slices, wsRepo)
	if len(got) != 2 {
		t.Fatalf("Schedule returned %d groups, want 2 (one per repo)", len(got))
	}
	want := [][]string{{"a"}, {"b"}}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("Schedule = %v, want %v", got, want)
	}
}

func TestScheduleOneRepoTwoWorkspacesSerial(t *testing.T) {
	slices := []store.Slice{
		{ID: "a", Workspace: "alpha"},
		{ID: "b", Workspace: "beta"},
		{ID: "c", Workspace: "alpha"},
	}
	wsRepo := map[string]string{"alpha": "fixture-repo", "beta": "fixture-repo"}

	got := Schedule(slices, wsRepo)
	if len(got) != 1 {
		t.Fatalf("Schedule returned %d groups, want 1 (single repo)", len(got))
	}
	want := []string{"a", "b", "c"}
	if !reflect.DeepEqual(got[0], want) {
		t.Fatalf("group = %v, want %v (slices.yaml order preserved)", got[0], want)
	}
}
