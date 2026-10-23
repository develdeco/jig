package migrate

import (
	"fmt"
	"os"
	"path/filepath"

	"gopkg.in/yaml.v3"

	"github.com/develdeco/jig/internal/axi"
	"github.com/develdeco/jig/internal/gitx"
	"github.com/develdeco/jig/internal/project"
	"github.com/develdeco/jig/internal/store"
)

// v3Work is what the v2 to v3 step works out in its plan and its apply carries
// out: the new store.yaml, and the marker to write in the project's repo.
type v3Work struct {
	storeYAML []byte
	// markerDir is the mapped clone of the project's one repo, and marker what
	// goes in its jig.yaml; markerExists says the clone already has one, which
	// the step leaves as it is.
	markerDir    string
	marker       project.Marker
	markerExists bool
}

// planV2toV3 works out the v2 to v3 step: a standalone project's project.yaml
// becomes the store's store.yaml (what says how tickets are written stays:
// schema_version, keys, trackers, platform, gate, context and any other key;
// name and repos go) and the project's name, repo and store remote move to a
// jig.yaml marker in the mapped clone of its repo. It reads the machine map
// and the clone, and changes nothing.
func planV2toV3(in Input) (Plan, error) {
	cfg := in.Cfg
	if cfg.SchemaVersion != 2 {
		return Plan{}, &axi.Error{
			Msg:  fmt.Sprintf("this store is at schema_version %d, not 2", cfg.SchemaVersion),
			Code: "VALIDATION_ERROR",
		}
	}
	switch len(cfg.Repos) {
	case 0:
		return Plan{}, &axi.Error{
			Msg:  "this store's project.yaml declares no repos: there is no repo to put the project's jig.yaml in",
			Code: "VALIDATION_ERROR",
		}
	case 1:
	default:
		return Plan{}, &axi.Error{
			Msg:  fmt.Sprintf("this store's project spans %d repos: migrating a multi-repo store to schema_version 3 is not supported yet", len(cfg.Repos)),
			Code: "VALIDATION_ERROR",
			Help: []string{"A project of several repos is an orchestrator (`jig init --orchestrator`), which this migration does not build"},
		}
	}
	repo := cfg.Repos[0]
	repoName := repo.Name()

	machine, err := project.LoadMachine(in.JigHome)
	if err != nil {
		return Plan{}, err
	}
	clone := machine[cfg.Name].Clones[repoName]
	if clone == "" {
		return Plan{}, &axi.Error{
			Msg:  fmt.Sprintf("this machine has no clone of repo %q mapped for project %s: the project's jig.yaml goes in it", repoName, cfg.Name),
			Code: "VALIDATION_ERROR",
			Help: []string{fmt.Sprintf("Map it: jig map %s=<path to your clone of the repo>", repoName)},
		}
	}
	if fi, err := os.Stat(clone); err != nil || !fi.IsDir() {
		return Plan{}, &axi.Error{
			Msg:  fmt.Sprintf("the clone of repo %q mapped for project %s, %s, is not a directory", repoName, cfg.Name, clone),
			Code: "VALIDATION_ERROR",
			Help: []string{fmt.Sprintf("Map the right one: jig map %s=<path>", repoName)},
		}
	}

	w := &v3Work{markerDir: clone}
	markerPath := filepath.Join(clone, project.MarkerFile)
	if _, err := os.Stat(markerPath); err == nil {
		m, err := project.LoadMarker(markerPath)
		if err != nil {
			return Plan{}, err
		}
		if m.Project != "" && m.Project != cfg.Name {
			return Plan{}, &axi.Error{
				Msg:  fmt.Sprintf("%s already marks project %q, not %q", markerPath, m.Project, cfg.Name),
				Code: "VALIDATION_ERROR",
			}
		}
		w.markerExists = true
	}
	w.marker = markerFor(in, cfg, repo, repoName, clone)

	data, err := os.ReadFile(filepath.Join(in.Store.Root, project.LegacyStoreFile))
	if err != nil {
		return Plan{}, err
	}
	if w.storeYAML, err = RewriteProjectYAMLToV3(data, cfg.Name); err != nil {
		return Plan{}, &axi.Error{Msg: err.Error(), Code: "VALIDATION_ERROR"}
	}

	files := []FileOp{
		{Action: "move", Path: project.StoreFile, From: project.LegacyStoreFile},
	}
	if w.markerExists {
		files = append(files, FileOp{Action: "keep", Path: filepath.ToSlash(markerPath)})
	} else {
		files = append(files, FileOp{Action: "write", Path: filepath.ToSlash(markerPath)})
	}
	return Plan{Files: files, v3: w}, nil
}

// markerFor is the jig.yaml of the project the v2 store described: its name,
// the kind project, and the store's remote when the store has one that is not a
// path on this machine (the file is committed, and a path on this machine
// means nothing to a collaborator). A repo entry is written only for what the
// repo's own clone does not already say: a name other than the project's, a
// target other than main, and a remote that is a URL but not the clone's origin.
func markerFor(in Input, cfg project.Config, repo project.Repo, repoName, clone string) project.Marker {
	m := project.Marker{Jig: "1", Project: cfg.Name, Kind: project.KindProject}
	if origin := project.OriginOf(in.Store.Root); origin != "" && !gitx.IsLocalRemote(origin) {
		m.Store = origin
	}
	var entry project.MarkerRepo
	if repoName != cfg.Name {
		entry.Name = repoName
	}
	if repo.Target != "" && repo.Target != "main" {
		entry.Target = repo.Target
	}
	if repo.Remote != "" && !gitx.IsLocalRemote(repo.Remote) {
		if origin := project.OriginOf(clone); origin == "" || filepath.ToSlash(origin) != filepath.ToSlash(repo.Remote) {
			entry.Remote = repo.Remote
		}
	}
	if entry != (project.MarkerRepo{}) {
		m.Repos = []project.MarkerRepo{entry}
	}
	return m
}

// checkV2toV3 has nothing to add to the refusals every step shares (a clean
// store, level with origin); the identity the commit needs is checked by the
// apply before it writes.
func checkV2toV3(Input, Plan) ([]string, error) { return nil, nil }

// applyV2toV3 carries out the plan: it writes the marker in the clone (unless
// there is one), swaps project.yaml for store.yaml and commits the swap once, as
// the person, on the current branch. The marker is the one file outside the
// store; it is left uncommitted in the person's repo for them to commit. A
// failure puts the store back as it was and takes away the marker it wrote.
func applyV2toV3(in Input, plan Plan) error {
	w := plan.v3
	if w == nil {
		return fmt.Errorf("migrate: the v2 to v3 plan is empty")
	}
	id, err := gitx.ResolveIdentity(in.Store.Root)
	if err != nil {
		return err
	}
	wroteMarker := false
	undo := func() {
		storeYAML := filepath.Join(in.Store.Root, project.StoreFile)
		_ = os.Remove(storeYAML)
		_, _ = gitx.Run(in.Store.Root, "reset", "-q", "--", project.StoreFile, project.LegacyStoreFile)
		_, _ = gitx.Run(in.Store.Root, "checkout", "--", project.LegacyStoreFile)
		if wroteMarker {
			_ = os.Remove(filepath.Join(w.markerDir, project.MarkerFile))
		}
	}
	fail := func(err error) error {
		undo()
		return &axi.Error{
			Msg:  fmt.Sprintf("jig store migrate failed, leaving the store at %s as it was: %v", in.Store.Root, err),
			Code: "STORE_MIGRATE_APPLY_FAILED",
		}
	}

	if !w.markerExists {
		if err := project.WriteMarker(w.markerDir, w.marker); err != nil {
			return fail(err)
		}
		wroteMarker = true
	}
	if err := store.AtomicWrite(filepath.Join(in.Store.Root, project.StoreFile), w.storeYAML); err != nil {
		return fail(err)
	}
	if err := os.Remove(filepath.Join(in.Store.Root, project.LegacyStoreFile)); err != nil {
		return fail(err)
	}
	if _, err := gitx.Run(in.Store.Root, "add", "-A", "--", project.StoreFile, project.LegacyStoreFile); err != nil {
		return fail(err)
	}
	if _, err := gitx.Run(in.Store.Root, "-c", "user.name="+id.Name, "-c", "user.email="+id.Email, "commit", "-m", "jig: migrate store to schema_version 3"); err != nil {
		return fail(err)
	}
	return nil
}

// helpV2toV3 says what is left to the person: commit the marker, push the
// store.
func helpV2toV3(_ Input, plan Plan) []string {
	help := []string{"Push the store's branch and review it before merging"}
	if w := plan.v3; w != nil {
		if w.markerExists {
			help = append(help, fmt.Sprintf("%s already had a %s: it was left as it is", w.markerDir, project.MarkerFile))
		} else {
			help = append(help, fmt.Sprintf("Commit %s in %s, so a clone of the repo finds the project", project.MarkerFile, w.markerDir))
		}
	}
	return help
}

// RewriteProjectYAMLToV3 rewrites data, a v2 project.yaml, as a v3 store.yaml
// for the project name: schema_version becomes 3, name: becomes projects:
// [name] in its own slot, repos: goes (the project's jig.yaml has them now),
// and every other key is left exactly as it is, comments included, since this
// edits the node tree rather than re-marshaling a Go struct.
func RewriteProjectYAMLToV3(data []byte, name string) ([]byte, error) {
	var doc yaml.Node
	if err := yaml.Unmarshal(data, &doc); err != nil {
		return nil, fmt.Errorf("migrate: parse project.yaml: %w", err)
	}
	if doc.Kind != yaml.DocumentNode || len(doc.Content) == 0 {
		return nil, fmt.Errorf("migrate: project.yaml is not a YAML document")
	}
	root := doc.Content[0]
	if root.Kind != yaml.MappingNode {
		return nil, fmt.Errorf("migrate: project.yaml's root is not a mapping")
	}

	projects := &yaml.Node{Kind: yaml.SequenceNode, Style: yaml.FlowStyle, Content: []*yaml.Node{
		{Kind: yaml.ScalarNode, Tag: "!!str", Value: name},
	}}
	if i := findKey(root, "name"); i >= 0 {
		old, oldValue := root.Content[i], root.Content[i+1]
		root.Content[i] = &yaml.Node{Kind: yaml.ScalarNode, Value: "projects", HeadComment: old.HeadComment, LineComment: old.LineComment, FootComment: old.FootComment}
		projects.LineComment = oldValue.LineComment
		root.Content[i+1] = projects
	} else {
		appendPair(root, "projects", projects)
	}
	if i := findKey(root, "repos"); i >= 0 {
		root.Content = append(root.Content[:i], root.Content[i+2:]...)
	}
	three := &yaml.Node{Kind: yaml.ScalarNode, Tag: "!!int", Value: "3"}
	if i := findKey(root, "schema_version"); i >= 0 {
		root.Content[i+1] = three
	} else {
		appendPair(root, "schema_version", three)
	}
	return yaml.Marshal(&doc)
}
