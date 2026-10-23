package recall

import (
	"reflect"
	"testing"
)

func TestFogItems(t *testing.T) {
	t.Parallel()
	cases := []struct {
		name string
		md   string
		want []string
	}{
		{
			"the template section, bullets with continuation lines",
			"# Map\n\n## Destination\n\nSomewhere.\n\n## Not yet specified (fog)\n\n- First question\n- Second question,\n  carried over\n* Third\n1. Fourth\n\n## Out of scope\n\n- Not fog\n",
			[]string{"First question", "Second question, carried over", "Third", "Fourth"},
		},
		{
			"paragraphs are items too, and a sub-heading ends none of them",
			"## Fog of war\n\nOne paragraph\nover two lines.\n\n### Later\n\nAnother paragraph.\n\n## Next\n",
			[]string{"One paragraph over two lines.", "Another paragraph."},
		},
		{
			"the section runs to the end of the map",
			"# Map\n\n### Not yet specified\n\n- Only item\n",
			[]string{"Only item"},
		},
		{
			"a fence hides headings and its lines are no items",
			"```\n## Not yet specified\n```\n\n## Notes\n\ntext\n",
			nil,
		},
		{
			"code in the section is no item",
			"## Not yet specified (fog)\n\n- before\n\n```\ncode line\n```\n\n- after\n",
			[]string{"before", "after"},
		},
		{"no fog section", "# Map\n\n## Destination\n\n- x\n", nil},
		{"an empty fog section", "## Not yet specified (fog)\n\n## Out of scope\n", nil},
		{"CRLF text", "## Not yet specified (fog)\r\n\r\n- one\r\n- two\r\n", []string{"one", "two"}},
	}
	for _, c := range cases {
		if got := FogItems(c.md); !reflect.DeepEqual(got, c.want) {
			t.Errorf("%s: FogItems = %q, want %q", c.name, got, c.want)
		}
	}
}

func TestClip(t *testing.T) {
	t.Parallel()
	if got := clip("short", 10); got != "short" {
		t.Errorf("clip of a short text = %q", got)
	}
	if got := clip("0123456789abcdef", 10); got != "0123456..." {
		t.Errorf("clip = %q, want 10 characters ending in an ellipsis", got)
	}
	if got := clip("héllo wörld, and more", 12); len([]rune(got)) != 12 {
		t.Errorf("clip of multibyte text = %q, want 12 runes", got)
	}
}
