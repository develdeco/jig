package manifest

import (
	"os"
	"path/filepath"
	"reflect"
	"sort"
	"strings"
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

// TestResolveTable covers the three v0.1 manifest-resolution cases: package.json-
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

func TestResolveInvariants(t *testing.T) {
	t.Run("declared invariants are carried", func(t *testing.T) {
		dir := t.TempDir()
		writeFile(t, filepath.Join(dir, "go.mod"), "module example.invalid/x\n\ngo 1.27\n")
		writeFile(t, filepath.Join(dir, ".claude", "jig.yaml"), `
invariants:
  - internal/store/
  - migrations/*.sql
`)
		m, err := Resolve(dir)
		if err != nil {
			t.Fatalf("Resolve: %v", err)
		}
		wantInvs := []string{"internal/store/", "migrations/*.sql"}
		if !reflect.DeepEqual(m.Invariants, wantInvs) {
			t.Errorf("Invariants = %v, want %v", m.Invariants, wantInvs)
		}
	})

	// A declaration validates in its path.Clean'ed form, so it must match in
	// that form too: "./migrations/*.sql" is a natural thing to write, and git
	// never reports a changed path with a "./" prefix, so an entry matched as
	// written would validate cleanly and then silently floor nothing.
	t.Run("declared invariants match in their cleaned form", func(t *testing.T) {
		dir := t.TempDir()
		writeFile(t, filepath.Join(dir, "go.mod"), "module example.invalid/x\n\ngo 1.27\n")
		writeFile(t, filepath.Join(dir, ".claude", "jig.yaml"), `
invariants:
  - ./internal/store/
  - ./migrations/*.sql
`)
		m, err := Resolve(dir)
		if err != nil {
			t.Fatalf("Resolve: %v", err)
		}
		for _, filePath := range []string{"internal/store/repo.go", "migrations/001.sql"} {
			if !m.MatchesInvariant(filePath) {
				t.Errorf("MatchesInvariant(%q) = false, want true: a declared entry that validates must match the paths git reports", filePath)
			}
		}
	})

	t.Run("no invariants when absent", func(t *testing.T) {
		dir := t.TempDir()
		writeFile(t, filepath.Join(dir, "go.mod"), "module example.invalid/x\n\ngo 1.27\n")
		m, err := Resolve(dir)
		if err != nil {
			t.Fatalf("Resolve: %v", err)
		}
		if len(m.Invariants) != 0 {
			t.Errorf("Invariants = %v, want empty", m.Invariants)
		}
	})

	t.Run("malformed pattern is rejected", func(t *testing.T) {
		dir := t.TempDir()
		writeFile(t, filepath.Join(dir, "go.mod"), "module example.invalid/x\n\ngo 1.27\n")
		writeFile(t, filepath.Join(dir, ".claude", "jig.yaml"), `
invariants:
  - "migrations/[invalid.sql"
`)
		_, err := Resolve(dir)
		if err == nil || !strings.Contains(err.Error(), "valid path.Match") {
			t.Errorf("Resolve: expected path.Match error, got %v", err)
		}
	})

	t.Run("absolute path is rejected", func(t *testing.T) {
		dir := t.TempDir()
		writeFile(t, filepath.Join(dir, "go.mod"), "module example.invalid/x\n\ngo 1.27\n")
		writeFile(t, filepath.Join(dir, ".claude", "jig.yaml"), `
invariants:
  - /absolute/path
`)
		_, err := Resolve(dir)
		if err == nil || !strings.Contains(err.Error(), "absolute") {
			t.Errorf("Resolve: expected absolute error, got %v", err)
		}
	})

	t.Run("path escaping repo is rejected", func(t *testing.T) {
		dir := t.TempDir()
		writeFile(t, filepath.Join(dir, "go.mod"), "module example.invalid/x\n\ngo 1.27\n")
		writeFile(t, filepath.Join(dir, ".claude", "jig.yaml"), `
invariants:
  - ../escapes
`)
		_, err := Resolve(dir)
		if err == nil || !strings.Contains(err.Error(), "escapes") {
			t.Errorf("Resolve: expected escapes error, got %v", err)
		}
	})
}

func TestMatchesInvariant(t *testing.T) {
	cases := []struct {
		name       string
		invariants []string
		filePath   string
		want       bool
	}{
		{"directory match: not the dir itself", []string{"internal/store/"}, "internal/store", false},
		{"directory match: file under dir", []string{"internal/store/"}, "internal/store/repo.go", true},
		{"directory match: subdir under dir", []string{"internal/store/"}, "internal/store/query/models.go", true},
		{"directory match: prefix but not under", []string{"internal/store/"}, "internal/storage/file.go", false},
		{"glob match: exact", []string{"migrations/*.sql"}, "migrations/001.sql", true},
		{"glob match: no match", []string{"migrations/*.sql"}, "migrations/001.go", false},
		{"glob match: deep path", []string{"migrations/*.sql"}, "src/migrations/001.sql", false},
		{"multiple invariants: first matches", []string{"internal/store/", "migrations/*.sql"}, "internal/store/repo.go", true},
		{"multiple invariants: second matches", []string{"internal/store/", "migrations/*.sql"}, "migrations/001.sql", true},
		{"multiple invariants: none match", []string{"internal/store/", "migrations/*.sql"}, "main.go", false},
		{"no invariants", []string{}, "internal/store/repo.go", false},
		// Entries git's own paths can never carry literally: each is matched
		// in the cleaned form validateInvariants already judges it in.
		{"glob with a ./ prefix", []string{"./migrations/*.sql"}, "migrations/001.sql", true},
		{"directory with a ./ prefix", []string{"./internal/store/"}, "internal/store/repo.go", true},
		{"directory with a ./ prefix: prefix but not under", []string{"./internal/store/"}, "internal/storage/file.go", false},
		{"entry with a redundant .. segment", []string{"internal/queue/../store/"}, "internal/store/repo.go", true},
		{"entry with a doubled separator", []string{"internal//store/"}, "internal/store/repo.go", true},
		{"the repo root as a directory entry covers every path", []string{"./"}, "main.go", true},
	}

	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			m := Manifest{Invariants: c.invariants}
			got := m.MatchesInvariant(c.filePath)
			if got != c.want {
				t.Errorf("MatchesInvariant(%q) = %v, want %v", c.filePath, got, c.want)
			}
		})
	}
}
