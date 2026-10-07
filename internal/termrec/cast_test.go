package termrec

import (
	"bytes"
	"reflect"
	"strings"
	"testing"
	"time"
)

// TestAsciicastRoundTrip: a Cast written as asciicast v2 reads back equal, to
// the microsecond, with its text as written.
func TestAsciicastRoundTrip(t *testing.T) {
	t.Parallel()
	want := Cast{Width: 80, Height: 24, Events: []Event{
		{Time: 0, Data: "$ jig gate T-1\r\n"},
		{Time: 1500 * time.Millisecond, Data: "\x1b[31m<&>\x1b[0m\r\n"},
		{Time: 1500*time.Millisecond + time.Microsecond, Data: "日本�"},
	}}
	var buf bytes.Buffer
	if err := want.WriteAsciicast(&buf); err != nil {
		t.Fatalf("WriteAsciicast: %v", err)
	}
	if first, _, _ := strings.Cut(buf.String(), "\n"); first != `{"version":2,"width":80,"height":24}` {
		t.Errorf("header = %s", first)
	}
	if !strings.Contains(buf.String(), `[1.500001,"o","日本�"]`) {
		t.Errorf("event lines do not carry seconds and text as written:\n%s", buf.String())
	}
	got, err := ReadAsciicast(&buf)
	if err != nil {
		t.Fatalf("ReadAsciicast: %v", err)
	}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("read back %+v, want %+v", got, want)
	}
}

// TestAsciicastCarriesARuneSplitAcrossWrites: a write that ends mid-rune
// cannot be a JSON string; the writer carries the partial rune into the next
// event, so the file draws what the terminal drew.
func TestAsciicastCarriesARuneSplitAcrossWrites(t *testing.T) {
	t.Parallel()
	c := Cast{Width: 10, Height: 2, Events: []Event{
		{Data: "a\xe6\x97"},
		{Time: time.Millisecond, Data: "\xa5!"},
		{Time: 2 * time.Millisecond, Data: "\xff\xe6"}, // invalid, then a rune the recording cuts off
	}}
	var buf bytes.Buffer
	if err := c.WriteAsciicast(&buf); err != nil {
		t.Fatalf("WriteAsciicast: %v", err)
	}
	read, err := ReadAsciicast(&buf)
	if err != nil {
		t.Fatalf("ReadAsciicast: %v", err)
	}
	want, _ := c.FinalText()
	got, err := read.FinalText()
	if err != nil {
		t.Fatalf("FinalText: %v", err)
	}
	if got != want || got != "a日!�" {
		t.Errorf("read back draws %q, the recording drew %q, want both %q", got, want, "a日!�")
	}
}

// TestReadAsciicastTakesARecorderOwnFile: asciinema's header fields and its
// input, marker and resize events are read past; output events are kept.
func TestReadAsciicastTakesARecorderOwnFile(t *testing.T) {
	t.Parallel()
	in := `{"version": 2, "width": 100, "height": 30, "timestamp": 1700000000, "env": {"SHELL": "/bin/sh", "TERM": "xterm-256color"}, "title": "demo"}
[0.25, "o", "hi"]
[0.5, "i", "q"]

[0.75, "m", "chapter"]
[1.0, "r", "90x20"]
[1.25, "o", "!\r\n"]
`
	got, err := ReadAsciicast(strings.NewReader(in))
	if err != nil {
		t.Fatalf("ReadAsciicast: %v", err)
	}
	want := Cast{Width: 100, Height: 30, Events: []Event{
		{Time: 250 * time.Millisecond, Data: "hi"},
		{Time: 1250 * time.Millisecond, Data: "!\r\n"},
	}}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("ReadAsciicast = %+v, want %+v", got, want)
	}
}

// TestReadAsciicastRefuses: what is not an asciicast v2 recording jig can
// render is an error that says why.
func TestReadAsciicastRefuses(t *testing.T) {
	t.Parallel()
	const header = `{"version":2,"width":80,"height":24}` + "\n"
	for _, tc := range []struct {
		name, in, want string
	}{
		{"an empty file", "", "empty file"},
		{"a header that is not JSON", "not json\n", "header"},
		{"version 1", `{"version":1,"width":80,"height":24}`, "version 1"},
		{"no width", `{"version":2,"height":24}`, "terminal size"},
		{"a terminal too tall", `{"version":2,"width":80,"height":201}`, "terminal size"},
		{"an event of two fields", header + `[1.0, "o"]`, "line 2"},
		{"a time that is not a number", header + `["1.0", "o", "x"]`, "line 2"},
		{"a negative time", header + `[-1, "o", "x"]`, "line 2"},
		{"data that is not a string", header + `[1.0, "o", 5]`, "line 2"},
		{"time going backwards", header + "[2.0, \"o\", \"a\"]\n[1.0, \"o\", \"b\"]", "comes before"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			_, err := ReadAsciicast(strings.NewReader(tc.in))
			if err == nil || !strings.Contains(err.Error(), tc.want) {
				t.Errorf("ReadAsciicast error = %v, want one naming %q", err, tc.want)
			}
		})
	}
}
