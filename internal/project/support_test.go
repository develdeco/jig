package project

import (
	"fmt"
	"time"

	"github.com/develdeco/jig/internal/home"
)

// LoadConfig is LoadConfigWith without a memo of the repos' origins.
func LoadConfig(jigHome, storeRoot, name string, roots ...string) (Config, error) {
	return LoadConfigWith(nil, jigHome, storeRoot, name, roots...)
}

// SaveMachine writes the per-machine project mapping under the jig home root
// jigHome, creating its parent directory if needed. It replaces the whole
// file, atomically and under the mapping's lock.
func SaveMachine(jigHome string, m map[string]MachineProject) error {
	return saveMachine(jigHome, m, machineLockTimeout)
}

// saveMachine is SaveMachine waiting up to wait for the lock.
func saveMachine(jigHome string, m map[string]MachineProject, wait time.Duration) error {
	if jigHome == "" {
		return fmt.Errorf("project: no jig home given")
	}
	release, err := lockFile(home.MachinePath(jigHome), "machine mapping", wait)
	if err != nil {
		return err
	}
	defer release()
	return writeMachine(jigHome, m)
}
