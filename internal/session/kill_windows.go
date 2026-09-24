//go:build windows

package session

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"time"
)

// taskkillDeadline bounds killTree's own call to taskkill. cmd.Cancel (see
// headless.go) runs before Go starts the dispatch's WaitDelay timer, so
// whatever killTree itself takes is not otherwise bounded by anything: a
// slow or wedged taskkill would extend the dispatch's timeout by that much.
// A real `taskkill /T /F` on a live tree returns in well under a second; a
// few seconds is generous headroom on a loaded machine while still keeping
// killTree's own worst case small next to the dispatch bound it backs.
const taskkillDeadline = 5 * time.Second

// taskkillPath resolves the absolute path to the real taskkill.exe instead
// of the bare name Command would otherwise resolve through PATH. PATH is
// searched left to right and a session has Edit/Write in the lease plus a
// shell: an unqualified "taskkill" trusts whatever the session's own commands
// put ahead of System32 on it. %SystemRoot% is how Windows itself names its
// install root; C:\Windows is the fallback for the rare process that has
// cleared its own environment.
func taskkillPath() string {
	root := os.Getenv("SystemRoot")
	if root == "" {
		root = `C:\Windows`
	}
	return filepath.Join(root, "System32", "taskkill.exe")
}

// killTree ends cmd's process and everything it started. On Windows a
// session's children (a shell per Bash call, a test runner, the tools it
// spawns) are not in a killable group of their own, and killing the CLI
// alone leaves them running and holding the pipes jig reads. taskkill /T
// walks the tree from the process id instead, run by its absolute path and
// under its own deadline so neither a shadowed PATH entry nor a taskkill
// that itself hangs can leave killTree - and the dispatch bound behind it -
// unbounded.
func killTree(cmd *exec.Cmd) error {
	if cmd.Process == nil {
		return nil
	}
	ctx, cancel := context.WithTimeout(context.Background(), taskkillDeadline)
	defer cancel()
	kill := exec.CommandContext(ctx, taskkillPath(), "/T", "/F", "/PID", strconv.Itoa(cmd.Process.Pid))
	if err := kill.Run(); err != nil {
		// taskkill is not there, denied, timed out, or the tree is already
		// gone: fall back to the process itself so the bound still ends
		// something.
		return cmd.Process.Kill()
	}
	return nil
}
