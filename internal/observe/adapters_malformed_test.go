package observe

import (
	"reflect"
	"testing"
)

func TestEveryAdapterRefusesWhatIsNotAJSONObject(t *testing.T) {
	t.Parallel()
	want := []string{"claude", "codex", "copilot", "cursor", "gemini", "neutral", "opencode"}
	if got := Harnesses(); !reflect.DeepEqual(got, want) {
		t.Fatalf("Harnesses() = %v, want %v", got, want)
	}
	for _, name := range Harnesses() {
		a, _ := AdapterFor(name)
		for _, bad := range []string{"", "   ", "not json", "[1,2]", `"text"`, "null", "{", "\x00\x01\x02", "{\"a\":"} {
			ev, err := a.Parse([]byte(bad))
			if err == nil || ev.Kind != "" {
				t.Errorf("%s.Parse(%q) = %+v, %v, want an error and no event", name, bad, ev, err)
			}
		}
		// A JSON object with nothing in it is not an error, and not an event.
		if ev, err := a.Parse([]byte(`{}`)); err != nil || ev.Kind != "" {
			t.Errorf("%s.Parse({}) = %+v, %v, want no event and no error", name, ev, err)
		}
		// A byte order mark in front is read through.
		if _, err := a.Parse([]byte("\xef\xbb\xbf{}")); err != nil {
			t.Errorf("%s.Parse with a BOM: %v", name, err)
		}
	}
}

func TestPayloadFieldsOfTheWrongTypeAreIgnored(t *testing.T) {
	t.Parallel()
	a, _ := AdapterFor("claude")
	ev, err := a.Parse([]byte(`{"hook_event_name":"PostToolUse","session_id":7,"cwd":["x"],"tool_name":{"a":1},"tool_input":"not an object","stop_hook_active":"yes","tool_response":{"exit_code":"1"}}`))
	if err != nil {
		t.Fatal(err)
	}
	if want := (Event{Harness: "claude", Kind: KindTool}); !reflect.DeepEqual(ev, want) {
		t.Errorf("Parse = %+v, want %+v", ev, want)
	}
}
