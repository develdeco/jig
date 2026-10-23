package setup

import (
	"slices"
	"strings"
	"testing"
)

func TestOrderedJSONRoundTrip(t *testing.T) {
	t.Parallel()
	in := `{
  "z": 1,
  "a": {"y": [1, 2.50, -3e2, true, false, null, "s\u00e9 & <b>"], "x": {}},
  "m": [],
  "z": 2
}
`
	v, err := parseJSON([]byte(in))
	if err != nil {
		t.Fatal(err)
	}
	root := v.(*object)
	if !slices.Equal(root.keys, []string{"z", "a", "m"}) {
		t.Errorf("keys = %v, want z a m (a repeated key keeps its first place)", root.keys)
	}
	if z, _ := root.get("z"); z.(interface{ String() string }).String() != "2" {
		t.Errorf("a repeated key kept %v, want the later value 2", z)
	}
	out, err := marshalJSON(v, detectLayout([]byte(in)))
	if err != nil {
		t.Fatal(err)
	}
	want := `{
  "z": 2,
  "a": {
    "y": [
      1,
      2.50,
      -3e2,
      true,
      false,
      null,
      "sé & <b>"
    ],
    "x": {}
  },
  "m": []
}
`
	if string(out) != want {
		t.Errorf("marshal =\n%s\nwant\n%s", out, want)
	}
	again, err := parseJSON(out)
	if err != nil {
		t.Fatal(err)
	}
	out2, _ := marshalJSON(again, detectLayout(out))
	if string(out2) != string(out) {
		t.Errorf("a second round trip changed the document:\n%s", out2)
	}
}

func TestObjectSetDelKeepOrder(t *testing.T) {
	t.Parallel()
	o := newObject()
	o.set("b", "1")
	o.set("a", "2")
	o.set("b", "3")
	o.set("c", "4")
	o.del("a")
	o.del("missing")
	if !slices.Equal(o.keys, []string{"b", "c"}) || o.len() != 2 {
		t.Errorf("keys = %v", o.keys)
	}
	if v, _ := o.get("b"); v != "3" {
		t.Errorf("b = %v, want it replaced in place", v)
	}
}

func TestParseJSONRefuses(t *testing.T) {
	t.Parallel()
	for _, in := range []string{"", "{", `{"a":}`, `{"a" 1}`, `[1,]`, `{1: 2}`, `{} x`, "// c\n{}"} {
		if _, err := parseJSON([]byte(in)); err == nil {
			t.Errorf("parseJSON(%q) = nil error", in)
		}
	}
}

func TestDetectLayout(t *testing.T) {
	t.Parallel()
	cases := []struct {
		name string
		in   string
		want layout
	}{
		{"empty", "", layout{indent: "  ", eol: "\n", finalNewline: true}},
		{"one line", `{"a":1}`, layout{indent: "  ", eol: "\n"}},
		{"four spaces", "{\n    \"a\": 1\n}\n", layout{indent: "    ", eol: "\n", finalNewline: true}},
		{"tabs crlf", "{\r\n\t\"a\": 1\r\n}\r\n", layout{indent: "\t", eol: "\r\n", finalNewline: true}},
		{"bom", "\xef\xbb\xbf{\n \"a\": 1\n}", layout{indent: " ", eol: "\n", bom: true}},
	}
	for _, c := range cases {
		if got := detectLayout([]byte(c.in)); got != c.want {
			t.Errorf("%s: layout = %+v, want %+v", c.name, got, c.want)
		}
	}
	if s := strings.TrimSpace(""); s != "" {
		t.Fatal("unreachable")
	}
}
