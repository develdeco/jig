package project

import (
	"bytes"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"

	"gopkg.in/yaml.v3"

	"github.com/develdeco/jig/internal/store"
)

// MarkerFile is the name of the file that marks a directory as a jig project's
// root: <dir>/jig.yaml, committed in the project's repo (or, for an
// orchestrator, in the directory above its member repos). It is not the
// per-repo manifest, which is <repo>/.claude/jig.yaml and which marker code
// never reads: FindMarker looks at a directory's own top level only, and
// skips a directory that is itself named .claude.
const MarkerFile = "jig.yaml"

// The kinds of directory a marker marks.
const (
	// KindProject: a repo that is the whole project, or the project's one
	// repo (the standalone shape).
	KindProject = "project"
	// KindOrchestrator: a directory above the project's member repos, which
	// sit nested inside it.
	KindOrchestrator = "orchestrator"
	// KindMember: a repo that belongs to an orchestrator's project; its
	// marker points up at the project and its store.
	KindMember = "member"
)

// Marker is a jig.yaml: what a clone says about the project it belongs to, so
// jig finds the project, and the store, from the clone alone. Every key is
// optional on read.
type Marker struct {
	// Jig is the version of the marker format.
	Jig string `yaml:"jig,omitempty"`
	// Project is the project's name, and Store the store's remote.
	Project string `yaml:"project,omitempty"`
	Store   string `yaml:"store,omitempty"`
	// Kind is KindProject, KindOrchestrator or KindMember.
	Kind string `yaml:"kind,omitempty"`
	// Expects is what the project expects of a person's setup.
	Expects *Expects `yaml:"expects,omitempty"`
	// Repos are the repos of an orchestrator's project.
	Repos []MarkerRepo `yaml:"repos,omitempty"`
	// Branches is how the project names its branches.
	Branches *BranchRule `yaml:"branches,omitempty"`
	// Budget is the default spend limit for a ticket.
	Budget *Budget `yaml:"budget,omitempty"`
	// Distribution says how the project's repos are shared among people.
	Distribution string `yaml:"distribution,omitempty"`
	// Unattended allows work to run without a person watching.
	Unattended bool `yaml:"unattended,omitempty"`
}

// MarkerRepo is one repo of a project: its name, where it sits relative to
// the marker's directory, its remote and the branch work lands on.
type MarkerRepo struct {
	Name   string `yaml:"name,omitempty"`
	Path   string `yaml:"path,omitempty"`
	Remote string `yaml:"remote,omitempty"`
	Target string `yaml:"target,omitempty"`
}

// Expects is what a project expects to be set up on a machine that works on
// it: the harnesses it is used from, and whether jig's hooks are installed.
type Expects struct {
	Harness []string `yaml:"harness,omitempty"`
	Hooks   bool     `yaml:"hooks,omitempty"`
}

// BranchRule is how branch names are derived: From names the tracker whose
// ids lead them.
type BranchRule struct {
	From string `yaml:"from,omitempty"`
}

// Budget is a spend limit: tokens and dollars.
type Budget struct {
	Tokens int64   `yaml:"tokens,omitempty"`
	USD    float64 `yaml:"usd,omitempty"`
}

// FindMarker looks for a marker walking up from start, one directory at a
// time to the filesystem root, and returns the directory of the nearest one
// with its decoded Marker. A marker nearer start wins over one above it, so a
// member repo's marker is found before its orchestrator's. ok is false, with
// no error, when there is none. start may be a directory or a file in one.
// The decoding is lenient: a key this jig does not know is ignored, so an
// older jig reads a newer marker. A marker that is not YAML at all is an
// error.
func FindMarker(start string) (dir string, m Marker, ok bool, err error) {
	dir, err = filepath.Abs(start)
	if err != nil {
		return "", Marker{}, false, fmt.Errorf("project: find marker from %s: %w", start, err)
	}
	if info, serr := os.Stat(dir); serr == nil && !info.IsDir() {
		dir = filepath.Dir(dir)
	}
	for {
		if filepath.Base(dir) != ".claude" {
			path := filepath.Join(dir, MarkerFile)
			if info, serr := os.Stat(path); serr == nil && !info.IsDir() {
				m, err := LoadMarker(path)
				if err != nil {
					return "", Marker{}, false, err
				}
				return dir, m, true, nil
			}
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			return "", Marker{}, false, nil
		}
		dir = parent
	}
}

// LoadMarker reads the marker file at path (the file itself, not its
// directory). Unknown keys are ignored; an empty file is an empty Marker.
func LoadMarker(path string) (Marker, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return Marker{}, fmt.Errorf("project: read marker: %w", err)
	}
	var m Marker
	if err := yaml.NewDecoder(bytes.NewReader(data)).Decode(&m); err != nil && !errors.Is(err, io.EOF) {
		return Marker{}, fmt.Errorf("project: parse marker %s: %w", path, err)
	}
	return m, nil
}

// WriteMarker writes m as dir's jig.yaml, atomically, creating dir if need be
// and replacing a marker already there whole. It takes no lock: the file sits
// in a person's repo, where a lock's sidecar file would show up as an
// untracked change.
func WriteMarker(dir string, m Marker) error {
	data, err := yaml.Marshal(m)
	if err != nil {
		return fmt.Errorf("project: marshal marker: %w", err)
	}
	if err := store.AtomicWrite(filepath.Join(dir, MarkerFile), data); err != nil {
		return fmt.Errorf("project: write marker: %w", err)
	}
	return nil
}
