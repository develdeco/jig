package migrate

import (
	"fmt"

	"github.com/develdeco/jig/internal/axi"
	"github.com/develdeco/jig/internal/project"
	"github.com/develdeco/jig/internal/store"
)

// Input is what a migration step works from: the store, its config as it is
// (so the version the step starts from), this machine's jig home (the machine
// map, the lease pool), and the operator's rename map when one was given.
type Input struct {
	Store   *store.Store
	Cfg     project.Config
	JigHome string
	// MapPath is the rename map file (--map), "" when none was given.
	MapPath string
}

// Step is one schema migration: it takes a store from schema_version From to
// From+1 and no further, so each migration stays one reviewable store pull
// request.
type Step struct {
	// From is the schema_version the step starts from.
	From int
	// Summary says what the step does, in a line.
	Summary string
	// UsesMap says whether the step reads a rename map (--map); a step that
	// does not refuses one, rather than ignore it.
	UsesMap bool
	// Plan reads what the step would do, refusing what it cannot, and changes
	// nothing: it is the dry run.
	Plan func(in Input) (Plan, error)
	// Check runs the refusals that stand between a plan and writing it, and
	// returns the warnings to print before it is carried out.
	Check func(in Input, plan Plan) (warnings []string, err error)
	// Apply carries the plan out and commits it once, on the store's current
	// branch, without pushing or syncing.
	Apply func(in Input, plan Plan) error
	// Help says what to do next, once the plan is applied.
	Help func(in Input, plan Plan) []string
}

// Steps are the migrations jig knows, by the schema_version each starts from.
// `jig store migrate` applies exactly the next one: a store at version v runs
// Steps[v] and is then at v+1; getting further is another run, and another
// pull request.
var Steps = map[int]Step{
	1: {
		From:    1,
		Summary: "move tickets under tickets/, give every ticket an area key, and make project.yaml schema_version 2",
		UsesMap: true,
		Plan:    planV1toV2,
		Check:   checkV1toV2,
		Apply:   applyV1toV2,
		Help: func(Input, Plan) []string {
			return []string{
				"Push the branch and review it before merging",
				"After the merge, the first sync updates each mirrored issue in place from its moved record",
			}
		},
	},
	2: {
		From:    2,
		Summary: "split project.yaml into store.yaml and the project's jig.yaml marker, and make the schema_version 3",
		Plan:    planV2toV3,
		Check:   checkV2toV3,
		Apply:   applyV2toV3,
		Help:    helpV2toV3,
	},
}

// NextStep is the step a store at cfg's schema_version runs. to is the
// schema_version asked for with --to, 0 for the next one: only the next is
// ever applied, so anything else is refused, naming the step that is next.
func NextStep(cfg project.Config, to int) (Step, error) {
	v := cfg.SchemaVersion
	switch {
	case v >= project.CurrentSchemaVersion:
		msg := fmt.Sprintf("this store is at schema_version %d, the newest this jig version knows", v)
		if v > project.CurrentSchemaVersion {
			msg = fmt.Sprintf("this store's schema_version %d is newer than this jig version understands (%d)", v, project.CurrentSchemaVersion)
		}
		return Step{}, &axi.Error{Msg: msg, Code: "VALIDATION_ERROR", Help: []string{"Nothing to migrate here"}}
	}
	step, ok := Steps[v]
	if !ok {
		return Step{}, &axi.Error{
			Msg:  fmt.Sprintf("jig has no migration from schema_version %d", v),
			Code: "VALIDATION_ERROR",
		}
	}
	if to != 0 && to != v+1 {
		return Step{}, &axi.Error{
			Msg:  fmt.Sprintf("--to %d: `jig store migrate` applies one step at a time, and this store's next step goes from schema_version %d to %d", to, v, v+1),
			Code: "VALIDATION_ERROR",
			Help: []string{fmt.Sprintf("Run it with --to %d (or without --to), merge the result, then run it again for the step after", v+1)},
		}
	}
	return step, nil
}

// planV1toV2 is the dry run of the v1 to v2 step: the operator's rename map
// checked against the store's tickets.
func planV1toV2(in Input) (Plan, error) {
	if err := CheckSchemaVersion(in.Cfg); err != nil {
		return Plan{}, err
	}
	if in.MapPath == "" {
		return Plan{}, &axi.Error{
			Msg:  "--map is required to migrate a store from schema_version 1",
			Code: "VALIDATION_ERROR",
			Help: []string{"Pass the rename map: `jig store migrate --map <file>` (--dry-run shows the plan first)"},
		}
	}
	m, err := LoadMap(in.MapPath)
	if err != nil {
		return Plan{}, err
	}
	return BuildPlan(in.Store, m)
}

// checkV1toV2 refuses a held lease and warns of each branch the rename leaves
// behind on a repo's origin.
func checkV1toV2(in Input, plan Plan) ([]string, error) {
	repoNames := make([]string, len(in.Cfg.Repos))
	for i, r := range in.Cfg.Repos {
		repoNames[i] = r.Name()
	}
	oldIDs := make([]string, len(plan.Renames))
	for i, r := range plan.Renames {
		oldIDs[i] = r.OldID
	}
	if err := CheckNoLeases(in.JigHome, repoNames, oldIDs); err != nil {
		return nil, err
	}
	return BranchWarnings(in.Store.Root, in.Cfg.Repos, plan.Renames), nil
}

func applyV1toV2(in Input, plan Plan) error { return Apply(in.Store, plan) }
