package lint

import (
	"bytes"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
)

// deadcodePin is the release the CI step runs: the oldest one measured to run
// clean on go.mod's Go on linux (v0.39.0, which the list was first measured
// with, panics there).
const deadcodePin = "v0.50.0"

// TestDeadcodeStepFailsOnAnyLine reads the deadcode step of the test job and
// runs its script with a stub go first on PATH: the step runs on Linux only,
// pins deadcode to an exact release over ./cmd/jig, fails with the list on
// stdout when deadcode prints a line, passes when it prints nothing, and fails
// when deadcode itself fails (a panic prints nothing and must not pass).
func TestDeadcodeStepFailsOnAnyLine(t *testing.T) {
	root := repoRoot(t)
	doc := loadWorkflow(t, root, "ci.yml")
	jobs, ok := yamlMap(doc["jobs"])
	if !ok {
		t.Fatal("ci.yml has no jobs")
	}
	job, ok := yamlMap(jobs["test"])
	if !ok {
		t.Fatal("ci.yml has no test job")
	}
	steps, _ := yamlSlice(job["steps"])
	var cond, script string
	found := 0
	for _, s := range steps {
		step, ok := yamlMap(s)
		if !ok {
			continue
		}
		if name, _ := yamlString(step["name"]); name == "deadcode" {
			found++
			cond, _ = yamlString(step["if"])
			script, _ = yamlString(step["run"])
		}
	}
	if found != 1 {
		t.Fatalf("the test job has %d steps named deadcode, want 1", found)
	}
	if cond != "runner.os == 'Linux'" {
		t.Errorf("the deadcode step's condition is %q, want runner.os == 'Linux'", cond)
	}
	pin := regexp.MustCompile(`golang\.org/x/tools/cmd/deadcode@(v\d+\.\d+\.\d+) \./cmd/jig`).FindStringSubmatch(script)
	if pin == nil || pin[1] != deadcodePin {
		t.Fatalf("the deadcode step's run line = %q, want deadcode@%s over ./cmd/jig, an exact release", script, deadcodePin)
	}

	bash := requireBash(t, "the deadcode step")
	for _, tc := range []struct {
		name     string
		stub     string
		wantFail bool
		wantOut  string
	}{
		{"a function only tests reach", "echo 'internal/x/x.go:1:6: unreachable func: Foo'", true, "unreachable func: Foo"},
		{"nothing printed", "true", false, ""},
		{"deadcode fails", "echo 'panic: unexpected expr' >&2; exit 2", true, ""},
	} {
		t.Run(tc.name, func(t *testing.T) {
			dir := t.TempDir()
			args := filepath.Join(dir, "args")
			stub := "#!/bin/sh\necho \"$@\" > '" + filepath.ToSlash(args) + "'\n" + tc.stub + "\n"
			if err := os.WriteFile(filepath.Join(dir, "go"), []byte(stub), 0o755); err != nil {
				t.Fatal(err)
			}
			cmd := exec.Command(bash, "-eo", "pipefail", "-c", script)
			cmd.Env = append(os.Environ(), "PATH="+dir+string(os.PathListSeparator)+os.Getenv("PATH"))
			var stdout bytes.Buffer
			cmd.Stdout = &stdout
			err := cmd.Run()
			if tc.wantFail && err == nil {
				t.Errorf("the step passed, want it to fail; stdout %q", stdout.String())
			}
			if !tc.wantFail && err != nil {
				t.Errorf("the step failed: %v; stdout %q", err, stdout.String())
			}
			if tc.wantOut != "" && !strings.Contains(stdout.String(), tc.wantOut) {
				t.Errorf("stdout = %q, want the list (%q)", stdout.String(), tc.wantOut)
			}
			got, _ := os.ReadFile(args)
			if want := "run golang.org/x/tools/cmd/deadcode@" + deadcodePin + " ./cmd/jig"; strings.TrimSpace(string(got)) != want {
				t.Errorf("go was run as %q, want %q", strings.TrimSpace(string(got)), want)
			}
		})
	}
}
