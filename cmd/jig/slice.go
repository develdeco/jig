package main

import (
	"fmt"
	"io"
	"os"
	"path/filepath"
	"slices"
	"strings"

	"github.com/develdeco/jig/internal/axi"
	"github.com/develdeco/jig/internal/journal"
	"github.com/develdeco/jig/internal/ready"
	"github.com/develdeco/jig/internal/store"
)

// The usage lines of `jig slice add`'s flags: the help row and the FlagSet
// carry the same text, which the help parity test compares.
const (
	sliceIDUsage        = "the new slice's id (required)"
	sliceGoalUsage      = "what the slice builds (required)"
	sliceOracleUsage    = "the command that is green when the slice is done (required)"
	sliceWorkspaceUsage = "workspace the slice builds in (default root when the project has one repo, else required)"
	sliceEnvUsage       = "env class the oracle needs"
	sliceBlockedUsage   = "comma-separated ids of the slices it waits for (each must exist)"
	sliceFromBriefUsage = "brief sections to stamp: Seams (the default), all, or a comma list of headings"
	sliceStoreUsage     = "explicit store path"
	sliceProjectUsage   = "project name, resolved via the machine mapping"
)

// defaultStampHeading is the section a new slice is stamped from unless told
// otherwise: the intake rule makes Seams every slice's anchor, so an edit of
// any other section never makes the slice stale.
const defaultStampHeading = "Seams"

// init registers this file's verb with the registry (registry.go).
func init() {
	registerVerb(verb{
		Spec: cmdSpec{"slice add", "add a slice to a ticket, stamped from its current brief: jig slice add <ticket> --id <id> --goal <text> --oracle <cmd>", []flagSpec{
			{"id", sliceIDUsage, false},
			{"goal", sliceGoalUsage, false},
			{"oracle", sliceOracleUsage, false},
			{"workspace", sliceWorkspaceUsage, false},
			{"env", sliceEnvUsage, false},
			{"blocked-by", sliceBlockedUsage, false},
			{"from-brief", sliceFromBriefUsage, false},
			{"store", sliceStoreUsage, false},
			{"project", sliceProjectUsage, false},
		}, false},
		Order: 191,
		Run: func(e env, args []string, stdout io.Writer, _ io.Reader) int {
			return cmdSliceAdd(e, args, stdout)
		},
		Probe: []string{"T-1"},
	})
}

// cmdSliceAdd implements `jig slice add <ticket> --id --goal --oracle`. The
// slice is appended to slices.yaml through the store's locked append, so a
// duplicate id is refused as it is for a gate's fix slice, and its state is
// written queued. The approval is not touched: it covers the brief, not the
// slice list.
func cmdSliceAdd(e env, args []string, stdout io.Writer) int {
	fs := newFlagSet(e, "slice add")
	id := fs.String("id", "", sliceIDUsage)
	goal := fs.String("goal", "", sliceGoalUsage)
	oracle := fs.String("oracle", "", sliceOracleUsage)
	workspace := fs.String("workspace", "", sliceWorkspaceUsage)
	envClass := fs.String("env", "", sliceEnvUsage)
	blockedBy := fs.String("blocked-by", "", sliceBlockedUsage)
	fromBrief := fs.String("from-brief", defaultStampHeading, sliceFromBriefUsage)
	storeFlag := fs.String("store", "", sliceStoreUsage)
	projectFlag := fs.String("project", "", sliceProjectUsage)
	positionals, handled, err := parseWithPositionals(stdout, fs, args)
	if handled {
		return 0
	} else if err != nil {
		return renderErr(stdout, err)
	}
	ticket, err := exactlyOne(positionals, "jig slice add", "ticket")
	if err != nil {
		return renderErr(stdout, err)
	}
	for _, req := range []struct{ name, val string }{{"id", *id}, {"goal", *goal}, {"oracle", *oracle}} {
		if strings.TrimSpace(req.val) == "" {
			return renderErr(stdout, &axi.Error{Msg: "jig slice add requires --" + req.name, Code: "VALIDATION_ERROR"})
		}
	}

	st, cfg, mp, _, err := resolveStoreForProject(e, *projectFlag, *storeFlag, stdout)
	if err != nil {
		return renderErr(stdout, err)
	}
	ticket, err = resolveTicketArg(st, ticket, stdout)
	if err != nil {
		return renderErr(stdout, err)
	}
	if err := requireTicket(st, ticket); err != nil {
		return renderErr(stdout, err)
	}
	if err := requireOpen(st, ticket); err != nil {
		return renderErr(stdout, err)
	}

	ws := strings.TrimSpace(*workspace)
	if ws == "" {
		if len(cfg.Repos) != 1 {
			return renderErr(stdout, &axi.Error{
				Msg:  "jig slice add requires --workspace: the project has more than one repo",
				Code: "VALIDATION_ERROR",
			})
		}
		ws = "root"
	}

	brief, err := os.ReadFile(filepath.Join(st.TicketDir(ticket), "brief.md"))
	if err != nil {
		if !os.IsNotExist(err) {
			return renderErr(stdout, err)
		}
		return renderErr(stdout, &axi.Error{
			Msg:  fmt.Sprintf("ticket %s has no brief.md, so there is no brief section to stamp the slice from", ticket),
			Code: "VALIDATION_ERROR",
			Help: []string{fmt.Sprintf("Plan %s first: a slice is stamped from its approved brief", ticket)},
		})
	}
	stamped, hashes, err := stampFrom(brief, *fromBrief)
	if err != nil {
		return renderErr(stdout, err)
	}

	existing, err := st.ReadSlices(ticket)
	if err != nil {
		return renderErr(stdout, err)
	}
	sl := store.Slice{
		ID:        strings.TrimSpace(*id),
		Workspace: ws,
		Goal:      strings.TrimSpace(*goal),
		Oracle:    strings.TrimSpace(*oracle),
		Env:       strings.TrimSpace(*envClass),
		BlockedBy: splitList(*blockedBy),
		FromBrief: hashes,
	}
	if sl.BlockedBy == nil {
		sl.BlockedBy = []string{}
	}
	if problems := newSliceProblems(existing, sl, ready.ProjectRepos(cfg, mp.Clones)); len(problems) > 0 {
		return renderErr(stdout, &axi.Error{
			Msg:  fmt.Sprintf("slice %s cannot be added to %s", sl.ID, ticket),
			Code: "VALIDATION_ERROR",
			Help: problems,
		})
	}

	if err := st.AppendSlices(ticket, []store.Slice{sl}); err != nil {
		return renderErr(stdout, err)
	}
	if err := st.WriteSliceState(ticket, sl.ID, store.SliceState{State: "queued"}); err != nil {
		return renderErr(stdout, err)
	}
	if err := journal.Append(st, ticket, journal.Line{
		TS: stamp(e), Slice: sl.ID, Event: journal.EventSliceAdd, Ref: sl.ID, Summary: strings.Join(stamped, ", "),
	}); err != nil {
		return renderErr(stdout, err)
	}
	if err := st.Push(fmt.Sprintf("%s: add slice %s", ticket, sl.ID)); err != nil {
		return renderErr(stdout, err)
	}

	axi.Render(stdout,
		axi.KV("slice", [][2]string{
			{"id", sl.ID},
			{"state", "queued"},
			{"workspace", sl.Workspace},
			{"blocked_by", strings.Join(sl.BlockedBy, ",")},
			{"from_brief", strings.Join(stamped, ", ")},
		}),
		axi.Help(fmt.Sprintf("Run `jig validate %s --check` to check the ticket with the new slice", ticket)),
	)
	return 0
}

// stampFrom resolves what --from-brief names against brief: the headings and
// their current hashes, in the brief's order. "all" is every section; any
// other value is a comma list of headings, each of which must exist.
func stampFrom(brief []byte, spec string) (headings, hashes []string, err error) {
	sections := store.BriefSections(brief)
	if len(sections) == 0 {
		return nil, nil, &axi.Error{Msg: `brief.md has no "## " sections to stamp the slice from`, Code: "VALIDATION_ERROR"}
	}
	var want []string
	if strings.TrimSpace(spec) == "all" {
		for _, sec := range sections {
			want = append(want, sec.Heading)
		}
	} else {
		want = splitList(spec)
	}
	if len(want) == 0 {
		return nil, nil, &axi.Error{Msg: "--from-brief names no section", Code: "VALIDATION_ERROR"}
	}
	current := store.BriefSectionHashes(brief)
	var known []string
	for _, sec := range sections {
		known = append(known, sec.Heading)
	}
	for _, h := range want {
		hash, ok := current[h]
		if !ok {
			return nil, nil, &axi.Error{
				Msg:  fmt.Sprintf("--from-brief: the brief has no section %q", h),
				Code: "VALIDATION_ERROR",
				Help: []string{"Its sections are: " + strings.Join(known, ", ")},
			}
		}
		if !slices.Contains(headings, h) {
			headings = append(headings, h)
			hashes = append(hashes, hash)
		}
	}
	return headings, hashes, nil
}

// splitList splits a comma list, trimming each item and dropping empty ones;
// it returns nil for none.
func splitList(s string) []string {
	var out []string
	for _, p := range strings.Split(s, ",") {
		if p = strings.TrimSpace(p); p != "" {
			out = append(out, p)
		}
	}
	return out
}

// newSliceProblems applies to a slice about to be added the checks `jig
// validate` applies to every slice - a non-empty oracle, existing blocked_by
// ids, no cycle, a workspace the repos know, and not the reserved id "gate" -
// and returns only what the new slice causes (a duplicate id is the store's to refuse): a problem the ticket already had
// is not this slice's to report.
func newSliceProblems(existing []store.Slice, sl store.Slice, repos []ready.Repo) []string {
	var problems []string
	if sl.ID == "gate" {
		problems = append(problems, `slice id "gate" is reserved for the gate reviewer's own dispatch, which uses it as the session's slice name`)
	}
	ids := map[string]bool{}
	for _, e := range existing {
		ids[e.ID] = true
	}
	for _, b := range sl.BlockedBy {
		if !ids[b] {
			problems = append(problems, fmt.Sprintf("blocked_by %q does not exist", b))
		}
	}
	if strings.TrimSpace(sl.Oracle) == "" {
		problems = append(problems, "oracle is empty")
	}
	all := append(slices.Clone(existing), sl)
	if cyc := findBlockedByCycle(all); cyc != "" && !slices.Contains(problems, "blocked_by cycle: "+cyc) {
		problems = append(problems, "blocked_by cycle: "+cyc)
	}
	before := map[string]bool{}
	for _, p := range validateWorkspaces(repos, existing) {
		before[p] = true
	}
	for _, p := range validateWorkspaces(repos, all) {
		if !before[p] {
			problems = append(problems, p)
		}
	}
	return problems
}
