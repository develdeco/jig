package observe

import (
	"encoding/json"
	"reflect"
	"strings"
	"testing"
)

func intp(n int) *int { return &n }

// parseCase is a payload and the event an adapter must make of it.
type parseCase struct {
	name    string
	payload string
	want    Event
}

func runParseCases(t *testing.T, harness string, cases []parseCase) {
	t.Helper()
	a, ok := AdapterFor(harness)
	if !ok {
		t.Fatalf("no adapter for %s", harness)
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got, err := a.Parse([]byte(c.payload))
			if err != nil {
				t.Fatalf("Parse: %v", err)
			}
			if !reflect.DeepEqual(got, c.want) {
				t.Errorf("Parse =\n  %+v\nwant\n  %+v", got, c.want)
			}
		})
	}
}

// renderCase is a result and the JSON an adapter must print for it ("" for
// nothing).
type renderCase struct {
	name string
	ev   Event
	res  Result
	want string
}

func runRenderCases(t *testing.T, harness string, cases []renderCase) {
	t.Helper()
	a, _ := AdapterFor(harness)
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got := a.Render(c.ev, c.res)
			if c.want == "" {
				if got != nil {
					t.Fatalf("Render = %s, want nothing", got)
				}
				return
			}
			var gotJSON, wantJSON any
			if err := json.Unmarshal(got, &gotJSON); err != nil {
				t.Fatalf("Render is not JSON: %q: %v", got, err)
			}
			if err := json.Unmarshal([]byte(c.want), &wantJSON); err != nil {
				t.Fatalf("bad want: %v", err)
			}
			if !reflect.DeepEqual(gotJSON, wantJSON) {
				t.Errorf("Render = %s\nwant     %s", got, c.want)
			}
			if strings.Count(strings.TrimRight(string(got), "\n"), "\n") != 0 {
				t.Errorf("Render is more than one line: %q", got)
			}
		})
	}
}
