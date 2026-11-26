package tui

import (
	"bytes"
	"reflect"
	"testing"

	"github.com/develdeco/jig/internal/axi"
)

// statusWithBrief is a status carrying the brief block parts given.
func statusWithBrief(kv [][2]string, header []string, rows [][]string, removed [][]string) string {
	parts := []string{"ticket: JIG-1", axi.KV("brief", kv), axi.Table("brief_sections", header, rows)}
	if removed != nil {
		parts = append(parts, axi.Table("brief_removed", []string{"heading"}, removed))
	}
	var out bytes.Buffer
	axi.Render(&out, parts...)
	return out.String()
}

// TestStatusBriefSectionsCarrySinceApproval: the status's since_approval
// column, brief_removed table and illustration_since_approval pair are read as
// they are said, and a status without them reads with none, so a section never
// shows a change the verb did not name.
func TestStatusBriefSectionsCarrySinceApproval(t *testing.T) {
	t.Parallel()
	with := parseStatus(statusWithBrief(
		[][2]string{{"approval", "changed"}, {"illustration_since_approval", "added"}},
		[]string{"heading", "slices", "since_approval"},
		[][]string{{"Goal", "-", "same"}, {"Totals", "a+b", "changed"}, {"Cart", "-", "-"}},
		[][]string{{"Old section"}},
	))
	b := with.Brief
	if b == nil {
		t.Fatal("no brief")
	}
	var since []string
	for _, s := range b.Sections {
		since = append(since, s.Since)
	}
	if want := []string{"same", "changed", ""}; !reflect.DeepEqual(since, want) {
		t.Errorf("Since = %q, want %q (a dash is nothing said)", since, want)
	}
	if want := []string{"Old section"}; !reflect.DeepEqual(b.Removed, want) {
		t.Errorf("Removed = %q, want %q", b.Removed, want)
	}
	if b.Illustration != "added" {
		t.Errorf("Illustration = %q, want added", b.Illustration)
	}

	without := parseStatus(statusWithBrief(
		[][2]string{{"approval", "current"}},
		[]string{"heading", "slices"},
		[][]string{{"Goal", "-"}},
		nil,
	)).Brief
	if without == nil || len(without.Sections) != 1 || without.Sections[0].Since != "" || len(without.Removed) != 0 || without.Illustration != "" {
		t.Errorf("a status without the parts reads %+v, want none of them", without)
	}
}
