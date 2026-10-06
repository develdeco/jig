package session

import (
	"os/exec"

	"github.com/develdeco/jig/internal/envrun"
)

// newProcessGroup and killTree are envrun's: a session and a bounded oracle
// run end their process trees the same way.
func newProcessGroup(cmd *exec.Cmd) { envrun.NewProcessGroup(cmd) }

func killTree(cmd *exec.Cmd) error { return envrun.KillTree(cmd) }
