package manifest

import (
	"os"
	"path/filepath"
	"reflect"
	"sort"
	"testing"
)

func writeFile(t *testing.T, path, content string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatalf("write %s: %v", path, err)
	}
}

func oracleNames(m Manifest) []string {
	names := make([]string, 0, len(m.Oracles))
	for k := range m.Oracles {
		names = append(names, k)
	}
	sort.Strings(names)
	return names
}

// TestResolveTable covers the three kickoff-mandated cases: package.json-
// derived oracles, jig.yaml workspace override, and envs coming only from
// jig.yaml.
func TestResolveTable(t *testing.T) {
	t.Run("package.json derives one oracle per script, no jig.yaml", func(t *testing.T) {
		dir := t.TempDir()
		writeFile(t, filepath.Join(dir, "package.json"), `{"scripts":{"test":"jest","lint":"eslint ."}}`)

		m, err := Resolve(dir)
		if err != nil {
			t.Fatalf("Resolve: %v", err)
		}
		want := map[string]string{"test": "npm run test", "lint": "npm run lint"}
		if !reflect.DeepEqual(m.Oracles, want) {
			t.Errorf("Oracles = %v, want %v", m.Oracles, want)
		}
		wantWS := []Workspace{{ID: "root", Path: "."}}
		if !reflect.DeepEqual(m.Workspaces, wantWS) {
			t.Errorf("Workspaces = %v, want %v", m.Workspaces, wantWS)
		}
		if len(m.Envs) != 0 {
			t.Errorf("Envs = %v, want empty", m.Envs)
		}
	})

	t.Run("declared jig.yaml workspaces replace detected", func(t *testing.T) {
		dir := t.TempDir()
		writeFile(t, filepath.Join(dir, "go.mod"), "module example.invalid/x\n\ngo 1.27\n")
		writeFile(t, filepath.Join(dir, ".claude", "jig.yaml"), `
workspaces:
  - id: alpha
    path: alpha
  - id: beta
    path: beta
`)
		m, err := Resolve(dir)
		if err != nil {
			t.Fatalf("Resolve: %v", err)
		}
		want := []Workspace{{ID: "alpha", Path: "alpha"}, {ID: "beta", Path: "beta"}}
		if !reflect.DeepEqual(m.Workspaces, want) {
			t.Errorf("Workspaces = %v, want %v", m.Workspaces, want)
		}
		// detected go.mod oracle survives since jig.yaml declared none.
		if m.Oracles["test"] != "go test ./..." {
			t.Errorf("Oracles[test] = %q, want detected default", m.Oracles["test"])
		}
	})

	t.Run("envs come only from jig.yaml, never detected", func(t *testing.T) {
		dir := t.TempDir()
		writeFile(t, filepath.Join(dir, "go.mod"), "module example.invalid/x\n\ngo 1.27\n")

		mNoDecl, err := Resolve(dir)
		if err != nil {
			t.Fatalf("Resolve: %v", err)
		}
		if len(mNoDecl.Envs) != 0 {
			t.Errorf("Envs without jig.yaml = %v, want empty", mNoDecl.Envs)
		}

		writeFile(t, filepath.Join(dir, ".claude", "jig.yaml"), `
envs:
  rig:
    up: "envtool up {port}"
    check: "envtool check"
    down: "envtool down"
    unavailable: defer-ci
`)
		m, err := Resolve(dir)
		if err != nil {
			t.Fatalf("Resolve: %v", err)
		}
		rig, ok := m.Envs["rig"]
		if !ok {
			t.Fatalf("Envs missing %q: %v", "rig", m.Envs)
		}
		if rig.Up != "envtool up {port}" || rig.Check != "envtool check" || rig.Down != "envtool down" || rig.Unavailable != "defer-ci" {
			t.Errorf("rig env = %+v, unexpected", rig)
		}
	})
}

func TestResolveOraclesMergeDeclaredWins(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, filepath.Join(dir, "go.mod"), "module example.invalid/x\n\ngo 1.27\n")
	writeFile(t, filepath.Join(dir, ".claude", "jig.yaml"), `
oracles:
  test: "go test ./{path}/..."
  lint: "golangci-lint run ./{path}/..."
`)
	m, err := Resolve(dir)
	if err != nil {
		t.Fatalf("Resolve: %v", err)
	}
	names := oracleNames(m)
	want := []string{"lint", "test"}
	if !reflect.DeepEqual(names, want) {
		t.Fatalf("oracle names = %v, want %v", names, want)
	}
	if m.Oracles["test"] != "go test ./{path}/..." {
		t.Errorf("declared oracle did not win: %q", m.Oracles["test"])
	}
}

func TestResolveNoManifestFiles(t *testing.T) {
	dir := t.TempDir()
	m, err := Resolve(dir)
	if err != nil {
		t.Fatalf("Resolve: %v", err)
	}
	if len(m.Oracles) != 0 {
		t.Errorf("Oracles = %v, want empty", m.Oracles)
	}
	want := []Workspace{{ID: "root", Path: "."}}
	if !reflect.DeepEqual(m.Workspaces, want) {
		t.Errorf("Workspaces = %v, want %v", m.Workspaces, want)
	}
}

func TestWorkspaceLookup(t *testing.T) {
	m := Manifest{Workspaces: []Workspace{{ID: "alpha", Path: "alpha"}}}
	if ws, ok := m.Workspace("alpha"); !ok || ws.Path != "alpha" {
		t.Fatalf("Workspace(alpha) = %+v, %v", ws, ok)
	}
	if _, ok := m.Workspace("missing"); ok {
		t.Fatalf("Workspace(missing) unexpectedly found")
	}
}

func TestOracleCmd(t *testing.T) {
	m := Manifest{Oracles: map[string]string{"test": "go test ./{path}/..."}}
	ws := Workspace{ID: "alpha", Path: "alpha"}

	if got := m.OracleCmd("test", ws); got != "go test ./alpha/..." {
		t.Errorf("OracleCmd(named) = %q", got)
	}
	if got := m.OracleCmd("./run-custom.sh", ws); got != "./run-custom.sh" {
		t.Errorf("OracleCmd(literal) = %q", got)
	}
}
