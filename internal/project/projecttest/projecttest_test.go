package projecttest

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/develdeco/jig/internal/project"
)

const demoProjectYAML = `
schema_version: 2
name: demo
keys:
  JIG: everything in demo
trackers: []
repos:
  - remote: https://example.invalid/org/demo.git
platform: platform/
`

func writeStore(t *testing.T) string {
	t.Helper()
	storeDir := t.TempDir()
	if err := os.WriteFile(filepath.Join(storeDir, "project.yaml"), []byte(demoProjectYAML), 0o644); err != nil {
		t.Fatal(err)
	}
	return storeDir
}

func TestInitProject(t *testing.T) {
	jigHome := t.TempDir()
	storeDir := writeStore(t)
	cloneDir := t.TempDir()

	cfg, err := InitProject(jigHome, storeDir, map[string]string{"demo": cloneDir})
	if err != nil {
		t.Fatalf("InitProject: %v", err)
	}
	if cfg.Name != "demo" {
		t.Fatalf("cfg.Name = %q, want demo", cfg.Name)
	}

	machine, err := project.LoadMachine(jigHome)
	if err != nil {
		t.Fatalf("LoadMachine: %v", err)
	}
	mp, ok := machine["demo"]
	if !ok {
		t.Fatalf("machine mapping missing demo: %+v", machine)
	}
	absStore, _ := filepath.Abs(storeDir)
	absClone, _ := filepath.Abs(cloneDir)
	if mp.Store != absStore {
		t.Errorf("mp.Store = %q, want %q", mp.Store, absStore)
	}
	if mp.Clones["demo"] != absClone {
		t.Errorf("mp.Clones[demo] = %q, want %q", mp.Clones["demo"], absClone)
	}
}

func TestInitProjectRejectsUnknownClone(t *testing.T) {
	if _, err := InitProject(t.TempDir(), writeStore(t), map[string]string{"other": t.TempDir()}); err == nil {
		t.Fatal("expected error for unknown clone name")
	}
}

// TestSaveMachineReplacesTheWholeMapping: what was mapped before is gone, what
// is given is there.
func TestSaveMachineReplacesTheWholeMapping(t *testing.T) {
	jigHome := t.TempDir()
	if err := SaveMachine(jigHome, map[string]project.MachineProject{"old": {Store: "/o"}}); err != nil {
		t.Fatal(err)
	}
	if err := SaveMachine(jigHome, map[string]project.MachineProject{"new": {Store: "/n"}}); err != nil {
		t.Fatal(err)
	}
	m, err := project.LoadMachine(jigHome)
	if err != nil {
		t.Fatal(err)
	}
	if len(m) != 1 || m["new"].Store != "/n" {
		t.Fatalf("mapping = %+v, want only new", m)
	}
}
