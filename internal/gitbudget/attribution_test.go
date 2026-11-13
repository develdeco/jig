package main

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func defParams(caller, dir string) string {
	return `{"event":"version","evt":"3"}` + "\n" +
		`{"event":"start","argv":["git","status"]}` + "\n" +
		fmt.Sprintf(`{"event":"def_param","scope":"command","param":"JIG_GIT_CALLER","value":%q}`, caller) + "\n" +
		fmt.Sprintf(`{"event":"def_param","scope":"command","param":"JIG_GIT_DIR","value":%q}`, dir) + "\n" +
		`{"event":"region_enter","label":"x"}` + "\n" +
		`{"event":"def_param","scope":"command","param":"JIG_GIT_CALLER","value":"late"}` + "\n" +
		`{"event":"exit","code":0}` + "\n"
}

func TestTestNameFromDir(t *testing.T) {
	cases := []struct{ dir, want string }{
		{"/tmp/TestFooBar123456789/001", "TestFooBar"},
		{"/tmp/TestFooBar_sub_case2371/001/work", "TestFooBar_sub_case"},
		{"/tmp/TestAVeryLongTestNameThatIsCutAtFortyCh987/001", "TestAVeryLongTestNameThatIsCutAtFortyCh"},
		{`C:\Users\x\AppData\Local\Temp\TestWin42\001\repo`, "TestWin"},
		{"/tmp/FuzzThing77/001", "FuzzThing"},
		{"/tmp/ExampleRun5", "ExampleRun"},
		{"/tmp/BenchmarkX9/001", "BenchmarkX"},
		{"/tmp/TestOuter1/TestInner2/001", "TestInner"},
		{"/tmp/jig-gittest123/clone", otherTest},
		{"", otherTest},
	}
	for _, c := range cases {
		if got := testNameFromDir(c.dir); got != c.want {
			t.Errorf("testNameFromDir(%q) = %q, want %q", c.dir, got, c.want)
		}
	}
}

func TestTraceHead(t *testing.T) {
	cases := []struct {
		name, body string
		want       traceHead
	}{
		{"both", defParams("pkg.F", "/tmp/TestX1/001"), traceHead{"pkg.F", "/tmp/TestX1/001"}},
		{"no def_param", `{"event":"version"}` + "\n" + `{"event":"exit","code":0}` + "\n", traceHead{}},
		{"truncated mid line", `{"event":"def_param","param":"JIG_GIT_CALLER","value":"pkg.G"}` + "\n" + `{"event":"def_par`, traceHead{caller: "pkg.G"}},
		{"empty", "", traceHead{}},
		{"after region_enter is ignored", `{"event":"region_enter"}` + "\n" + `{"event":"def_param","param":"JIG_GIT_CALLER","value":"late"}` + "\n", traceHead{}},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			p := filepath.Join(t.TempDir(), "trace")
			if err := os.WriteFile(p, []byte(c.body), 0o644); err != nil {
				t.Fatal(err)
			}
			got, err := readTraceHead(p)
			if err != nil || got != c.want {
				t.Errorf("readTraceHead = %+v, %v; want %+v", got, err, c.want)
			}
		})
	}
	if _, err := readTraceHead(filepath.Join(t.TempDir(), "missing")); err == nil {
		t.Error("a missing file is an error")
	}
}

func TestTraceHeadStopsReading(t *testing.T) {
	// A long tail after the head must not matter: the head is complete at
	// the second def_param.
	p := filepath.Join(t.TempDir(), "trace")
	body := defParams("pkg.F", "/tmp/TestX1") + strings.Repeat("x", 1<<20)
	if err := os.WriteFile(p, []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
	if h, err := readTraceHead(p); err != nil || h.caller != "pkg.F" {
		t.Errorf("got %+v, %v", h, err)
	}
}

func TestAttribution(t *testing.T) {
	dir := t.TempDir()
	write := func(name, body string) {
		if err := os.WriteFile(filepath.Join(dir, name), []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	write("a", defParams("pkg.A", "/tmp/TestOne12/001"))
	write("b", defParams("pkg.A", "/tmp/TestOne99/002"))
	write("c", defParams("pkg.B", "/tmp/elsewhere"))
	write("d", `{"event":"exit","code":0}`+"\n")
	write("e", "")
	write("f", `{"event":"def_param","param":"JIG_GIT_CALLER","value":"pkg.T"}`+"\n"+`{"event":"def_par`)
	// A directory with no caller still names its test.
	write("g", `{"event":"def_param","param":"JIG_GIT_DIR","value":"/tmp/TestOne5/001"}`+"\n"+`{"event":"exit","code":0}`+"\n")

	got, err := attribute(dir)
	if err != nil {
		t.Fatal(err)
	}
	n, err := countTraces(dir)
	if err != nil || got.total != n || n != 7 {
		t.Fatalf("total = %d, countTraces = %d, %v", got.total, n, err)
	}
	wantCallers := map[string]int{"pkg.A": 2, "pkg.B": 1, "pkg.T": 1, unattributed: 3}
	wantTests := map[string]int{"TestOne": 3, otherTest: 2, unattributed: 2}
	for name, c := range map[string]struct{ got, want map[string]int }{"callers": {got.callers, wantCallers}, "tests": {got.tests, wantTests}} {
		sum := 0
		for k, v := range c.want {
			sum += v
			if c.got[k] != v {
				t.Errorf("%s[%q] = %d, want %d", name, k, c.got[k], v)
			}
		}
		if len(c.got) != len(c.want) || sum != n {
			t.Errorf("%s = %v, want %v summing to %d", name, c.got, c.want, n)
		}
	}
	if none, err := attribute(filepath.Join(dir, "never")); err != nil || none.total != 0 {
		t.Errorf("missing dir: %+v, %v", none, err)
	}
}
