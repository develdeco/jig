package lint

import (
	"bytes"
	"os/exec"
	"path/filepath"
	"runtime"
	"sort"
	"strings"
	"testing"
)

// modulePath is the module's import path prefix.
const modulePath = "github.com/develdeco/jig"

// cliForbiddenDeps are the packages the jig binary must not link: the ones
// that start a coding session and the code that only they use. jig watches
// the sessions of other tools (the hooks) and verifies what they commit; it
// does not start one, so no code of that kind is in the binary, whether or not
// anything calls it.
var cliForbiddenDeps = []string{
	"internal/session",
	"internal/staircase",
	"internal/screen",
	"internal/frontier",
}

// TestCLIDoesNotLinkSession pins that the binary links no session code: `go
// list -deps ./cmd/jig`, for each of the platforms jig is built for, names
// none of the packages in cliForbiddenDeps. It also names the module's
// packages the binary does link, so a list that came back empty or short
// fails this test instead of passing it.
func TestCLIDoesNotLinkSession(t *testing.T) {
	root := repoRoot(t)
	goBin := filepath.Join(runtime.GOROOT(), "bin", "go")
	if runtime.GOOS == "windows" {
		goBin += ".exe"
	}
	for _, goos := range []string{"linux", "darwin", "windows"} {
		t.Run(goos, func(t *testing.T) {
			t.Parallel()
			cmd := exec.Command(goBin, "list", "-deps", "-f", "{{.ImportPath}}", "./cmd/jig")
			cmd.Dir = root
			cmd.Env = append(cmd.Environ(), "GOOS="+goos, "CGO_ENABLED=0")
			var stdout, stderr bytes.Buffer
			cmd.Stdout, cmd.Stderr = &stdout, &stderr
			if err := cmd.Run(); err != nil {
				t.Fatalf("go list -deps ./cmd/jig (GOOS=%s): %v\n%s", goos, err, stderr.String())
			}
			linked := map[string]bool{}
			for _, p := range strings.Fields(stdout.String()) {
				linked[p] = true
			}
			for _, want := range []string{"internal/store", "internal/verifydeliver", "internal/observe", "internal/ready"} {
				if !linked[modulePath+"/"+want] {
					var some []string
					for p := range linked {
						if strings.HasPrefix(p, modulePath) {
							some = append(some, p)
						}
					}
					sort.Strings(some)
					t.Fatalf("go list -deps ./cmd/jig (GOOS=%s) does not name %s, so the list is not the binary's; it names %d of the module's packages: %v", goos, want, len(some), some)
				}
			}
			for _, pkg := range cliForbiddenDeps {
				if linked[modulePath+"/"+pkg] {
					t.Errorf("cmd/jig links %s (GOOS=%s): the binary starts no session, so nothing it imports may; find the import with `go list -deps -f '{{.ImportPath}}: {{.Imports}}' ./cmd/jig`", pkg, goos)
				}
			}
		})
	}
}
