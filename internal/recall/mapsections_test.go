package recall

import (
	"reflect"
	"testing"
)

func TestParseMap(t *testing.T) {
	t.Parallel()
	full := "# Coupons at checkout\n\n## Destination\n\nA coupon takes money off an order.\n\n## Notes\n\nProse.\n\n## Decisions so far\n\n- One coupon per order\n- Codes are case-insensitive\n\n## Not yet specified (fog)\n\n- Stacking with sale prices\n\n## Out of scope\n\n- Gift cards\n"
	got := ParseMap(full)
	want := MapSections{
		Title:       "Coupons at checkout",
		Destination: []string{"A coupon takes money off an order."},
		Decided:     []string{"One coupon per order", "Codes are case-insensitive"},
		Fog:         []string{"Stacking with sale prices"},
		OutOfScope:  []string{"Gift cards"},
	}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("ParseMap(full) = %+v, want %+v", got, want)
	}

	// A map with other headings, and one with no sections at all, read as empty
	// rather than failing.
	odd := ParseMap("# Only a title\n\n## Whatever\n\n- x\n")
	if odd.Title != "Only a title" || len(odd.Destination)+len(odd.Decided)+len(odd.Fog)+len(odd.OutOfScope) != 0 {
		t.Errorf("ParseMap(odd) = %+v, want a title and nothing else", odd)
	}
	if empty := ParseMap(""); empty.Title != "" || empty.Fog == nil || len(empty.Fog) != 0 {
		t.Errorf("ParseMap(\"\") = %+v, want empty, non-nil sections", empty)
	}

	// A title in a fence is no title, and the first top-level heading wins.
	if got := ParseMap("```\n# not a title\n```\n\n# Real\n\n# Second\n").Title; got != "Real" {
		t.Errorf("title = %q, want Real", got)
	}
}

func TestSectionItemsMatchesTheFirstSection(t *testing.T) {
	t.Parallel()
	md := "## Decisions so far\n\n- a\n\n## Decisions so far (again)\n\n- b\n"
	got := SectionItems(md, headingStarts("decisions"))
	if !reflect.DeepEqual(got, []string{"a"}) {
		t.Errorf("SectionItems = %v, want the first section's items", got)
	}
}
