package axi

import (
	"bytes"
	"errors"
	"testing"
)

func TestKV(t *testing.T) {
	got := KV("ticket", [][2]string{{"id", "JIG-1"}, {"state", "green"}})
	want := "ticket:\n  id: JIG-1\n  state: green"
	if got != want {
		t.Fatalf("KV = %q, want %q", got, want)
	}
}

func TestKVQuotesValue(t *testing.T) {
	got := KV("info", [][2]string{{"note", "a, b: c"}})
	want := "info:\n  note: \"a, b: c\""
	if got != want {
		t.Fatalf("KV = %q, want %q", got, want)
	}
}

func TestTable(t *testing.T) {
	got := Table("slices", []string{"id", "state"}, [][]string{{"a", "green"}, {"b", "queued"}})
	want := "slices[2]{id,state}:\n  a,green\n  b,queued"
	if got != want {
		t.Fatalf("Table = %q, want %q", got, want)
	}
}

func TestTableEmpty(t *testing.T) {
	got := Table("slices", []string{"id", "state"}, nil)
	want := "slices[0]{id,state}:"
	if got != want {
		t.Fatalf("Table = %q, want %q", got, want)
	}
}

func TestTableQuotesCells(t *testing.T) {
	got := Table("q", []string{"id", "body"}, [][]string{{"q-001", "yes, no"}})
	want := "q[1]{id,body}:\n  q-001,\"yes, no\""
	if got != want {
		t.Fatalf("Table = %q, want %q", got, want)
	}
}

func TestHelp(t *testing.T) {
	got := Help("Run `jig run JIG-1` to continue")
	want := "help[1]:\n  Run `jig run JIG-1` to continue"
	if got != want {
		t.Fatalf("Help = %q, want %q", got, want)
	}
}

func TestHelpEmpty(t *testing.T) {
	got := Help()
	want := "help[0]:"
	if got != want {
		t.Fatalf("Help = %q, want %q", got, want)
	}
}

func TestQuoteUnchangedWhenPlain(t *testing.T) {
	if got := Quote("plain"); got != "plain" {
		t.Fatalf("Quote = %q, want %q", got, "plain")
	}
}

func TestQuoteCases(t *testing.T) {
	cases := []struct{ in, want string }{
		{"a,b", `"a,b"`},
		{"a:b", `"a:b"`},
		{`a"b`, `"a\"b"`},
		{`a\b`, `"a\b"`},
		{"a\nb", `"a\nb"`},
	}
	for _, c := range cases {
		if got := Quote(c.in); got != c.want {
			t.Errorf("Quote(%q) = %q, want %q", c.in, got, c.want)
		}
	}
}

func TestRender(t *testing.T) {
	var buf bytes.Buffer
	Render(&buf, KV("ticket", [][2]string{{"id", "JIG-1"}}), Help("next step"))
	want := "ticket:\n  id: JIG-1\nhelp[1]:\n  next step\n"
	if buf.String() != want {
		t.Fatalf("Render = %q, want %q", buf.String(), want)
	}
}

func TestRenderError(t *testing.T) {
	var buf bytes.Buffer
	RenderError(&buf, &Error{Msg: "bad brief", Code: "VALIDATION_ERROR", Help: []string{"fix brief.md"}})
	want := "error: bad brief\ncode: VALIDATION_ERROR\nhelp[1]:\n  fix brief.md\n"
	if buf.String() != want {
		t.Fatalf("RenderError = %q, want %q", buf.String(), want)
	}
}

func TestRenderErrorNoHelp(t *testing.T) {
	var buf bytes.Buffer
	RenderError(&buf, &Error{Msg: "boom", Code: "INTERNAL"})
	want := "error: boom\ncode: INTERNAL\n"
	if buf.String() != want {
		t.Fatalf("RenderError = %q, want %q", buf.String(), want)
	}
}

func TestExitCode(t *testing.T) {
	cases := []struct {
		err  error
		want int
	}{
		{nil, 0},
		{&Error{Msg: "x", Code: "VALIDATION_ERROR"}, 2},
		{&Error{Msg: "x", Code: NeedsInput}, 2},
		{&Error{Msg: "x", Code: "PUSH_REFUSED"}, 1},
		{errors.New("plain"), 1},
	}
	for _, c := range cases {
		if got := ExitCode(c.err); got != c.want {
			t.Errorf("ExitCode(%v) = %d, want %d", c.err, got, c.want)
		}
	}
}
