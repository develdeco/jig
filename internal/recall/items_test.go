package recall

import (
	"reflect"
	"testing"
)

// TestItemsSkipTableRows: a table's lines, header and separator included, are
// no item of any section, and bullets around the table stay.
func TestItemsSkipTableRows(t *testing.T) {
	t.Parallel()
	md := "# Map\n\n## Decisions so far\n\n- first\n\n| id | what |\n|----|------|\n| 1 | one |\n\n- second\n\n## Not yet specified (fog)\n\n| id | what |\n|----|------|\n| 1 | one |\n\n## Out of scope\n\n- gift cards\n| a | b |\n"
	got := ParseMap(md)
	if want := []string{"first", "second"}; !reflect.DeepEqual(got.Decided, want) {
		t.Errorf("Decided = %q, want %q", got.Decided, want)
	}
	if len(got.Fog) != 0 {
		t.Errorf("a fog with only a table has items %q", got.Fog)
	}
	if want := []string{"gift cards"}; !reflect.DeepEqual(got.OutOfScope, want) {
		t.Errorf("OutOfScope = %q, want %q", got.OutOfScope, want)
	}
	if got := FogItems(md); len(got) != 0 {
		t.Errorf("FogItems = %q, want none", got)
	}
	if got := SectionItems(md, func(h string) bool { return h == "Decisions so far" }); !reflect.DeepEqual(got, []string{"first", "second"}) {
		t.Errorf("SectionItems = %q", got)
	}
}

// TestItemsSkipNone: an item that only says none is no item, in a paragraph or
// a bullet; one that merely starts with None is kept.
func TestItemsSkipNone(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct {
		name, fog string
		want      []string
	}{
		{"a paragraph", "None.\n", nil},
		{"a bullet", "- none\n", nil},
		{"none yet", "- None yet.\n", nil},
		{"n/a", "N/A\n", nil},
		{"upper case", "- NONE\n", nil},
		{"starting with none", "None of these block it\n", []string{"None of these block it"}},
		{"a bullet starting with none", "- none of these\n- real\n", []string{"none of these", "real"}},
		{"none among items", "- real\n- none\n", []string{"real"}},
	} {
		md := "## Not yet specified (fog)\n\n" + tc.fog
		if got := ParseMap(md).Fog; len(got) != len(tc.want) || (len(got) > 0 && !reflect.DeepEqual(got, tc.want)) {
			t.Errorf("%s: Fog = %q, want %q", tc.name, got, tc.want)
		}
		if got := FogItems(md); len(got) != len(tc.want) || (len(got) > 0 && !reflect.DeepEqual(got, tc.want)) {
			t.Errorf("%s: FogItems = %q, want %q", tc.name, got, tc.want)
		}
	}
}
