package main

import (
	"fmt"
	"io"
	"os"
	"strconv"
	"strings"

	"github.com/develdeco/jig/internal/axi"
	"github.com/develdeco/jig/internal/locate"
	"github.com/develdeco/jig/internal/store"
	"github.com/develdeco/jig/internal/verifydeliver"
)

// The usage lines of `jig review context` and `jig review done`: the help
// rows and the FlagSets carry the same text, which the help parity test
// compares.
const (
	reviewSessionUsage  = "the reviewer's harness session id (default: $JIG_SESSION, else CLAUDE_CODE_SESSION_ID); the round is judged independent of the builders by it"
	reviewModelUsage    = "the model the reviewer runs on, as it reports it; jig chooses none"
	reviewEffortUsage   = "the reasoning effort the reviewer runs at, as it reports it"
	reviewJSONUsage     = "print the review request as JSON (the review.json the round wrote)"
	reviewResultUsage   = "the reviewer's whole report as a result.json (findings, still_present, reviewed_paths, summary), instead of findings reported with `jig finding`"
	reviewReviewedUsage = "a repo-relative path the reviewer read, as <repo>/<path> in a project of several repos (repeat the flag, or separate with commas); every must_review path must be covered"
	reviewSummaryUsage  = "the reviewer's summary of the round, in a sentence or two"
	reviewStoreUsage    = "explicit store path"
	reviewProjectUsage  = "project name, resolved via the machine mapping"
)

// init registers this file's verbs with the registry (registry.go).
func init() {
	registerVerb(verb{
		Spec: cmdSpec{"review context", "read the open gate round: what to review, against what, and how to report", []flagSpec{
			{"session", reviewSessionUsage, false},
			{"model", reviewModelUsage, false},
			{"effort", reviewEffortUsage, false},
			{"json", reviewJSONUsage, false},
			{"store", reviewStoreUsage, false},
			{"project", reviewProjectUsage, false},
		}, false},
		Order: 330,
		Run: func(e env, args []string, stdout io.Writer, _ io.Reader) int {
			return cmdReviewContext(e, args, stdout)
		},
		Probe: []string{"T-1"},
	})
	registerVerb(verb{
		Spec: cmdSpec{"review done", "close the open gate round with the reviewer's report", []flagSpec{
			{"result", reviewResultUsage, false},
			{"reviewed", reviewReviewedUsage, false},
			{"summary", reviewSummaryUsage, false},
			{"session", reviewSessionUsage, false},
			{"store", reviewStoreUsage, false},
			{"project", reviewProjectUsage, false},
		}, false},
		Order: 332,
		Run: func(e env, args []string, stdout io.Writer, _ io.Reader) int {
			return cmdReviewDone(e, args, stdout)
		},
		Probe: []string{"T-1"},
	})
}

// roundBlocks renders an open round: where it stands (the branch, whose copy
// of it, the head under review and what it is reviewed against), where to read
// the code and the request, what the intent is, and who has said they review
// it. A round over the repos of a project of several says that of each repo, in
// a table of its own. moved is the repos whose clone's branch moved since the
// round opened, with the commit it is at now, none otherwise. full adds the
// tables a reviewer reads: the files it must cover, the findings still open or
// dismissed, and the oracles that passed.
func roundBlocks(p verifydeliver.Prepared, moved []verifydeliver.RepoTip, full bool) []string {
	req := p.Request
	kv := [][2]string{
		{"ticket", p.Ticket},
		{"round", strconv.Itoa(p.Round)},
		{"branch", p.Branch},
	}
	if len(p.Repos) == 0 {
		kv = append(kv,
			[2]string{"copy", p.Copy},
			[2]string{"head", p.Head},
			[2]string{"base", p.Base},
			[2]string{"scope", p.Scope},
			[2]string{"worktree", p.Lease},
		)
	}
	kv = append(kv,
		[2]string{"request", p.RequestPath},
		[2]string{"intent", p.Intent.Source},
	)
	if p.Intent.Path != "" {
		kv = append(kv, [2]string{"intent_path", p.Intent.Path})
	}
	if req.IntentExcerpt != "" {
		kv = append(kv, [2]string{"intent_excerpt", req.IntentExcerpt})
	} else if p.IntentNote != "" {
		kv = append(kv, [2]string{"intent_note", p.IntentNote})
	}
	if req.Recordings != "" {
		kv = append(kv, [2]string{"recordings", req.Recordings})
	}
	kv = append(kv, [2]string{"must_review", strconv.Itoa(len(req.MustReview))})
	if p.Reviewer.Known() {
		kv = append(kv, [2]string{"reviewer", reviewerLabel(p.Reviewer)})
	}
	if p.Reviewer.Model != "" {
		kv = append(kv, [2]string{"model", p.Reviewer.Model})
	}
	if p.Reviewer.Effort != "" {
		kv = append(kv, [2]string{"effort", p.Reviewer.Effort})
	}
	if len(moved) > 0 {
		kv = append(kv, [2]string{"stale", staleText(p, moved)})
	}
	blocks := []string{axi.KV("review", kv)}
	if len(p.Repos) > 0 {
		mustBy := map[string]int{}
		for _, rr := range req.Repos {
			mustBy[rr.Repo] = len(rr.MustReview)
		}
		var repoRows [][]string
		for _, r := range p.Repos {
			repoRows = append(repoRows, []string{r.Repo, r.Copy, r.Head, r.Base, r.Scope, r.Lease, strconv.Itoa(mustBy[r.Repo])})
		}
		blocks = append(blocks, axi.Table("repos", []string{"repo", "copy", "head", "base", "scope", "worktree", "must_review"}, repoRows))
	}
	if !full {
		return blocks
	}

	mustRows := make([][]string, len(req.MustReview))
	for i, path := range req.MustReview {
		mustRows[i] = []string{path}
	}
	blocks = append(blocks, axi.Table("must_review", []string{"path"}, mustRows))
	var openRows [][]string
	for _, f := range req.Open {
		openRows = append(openRows, []string{f.ID, fmt.Sprintf("%s:%d", f.File, f.Line), f.Action, f.Risk, f.Title, f.DecisionText()})
	}
	blocks = append(blocks, axi.Table("open", []string{"id", "file:line", "action", "risk", "title", "decision"}, openRows))
	var dismissedRows [][]string
	for _, f := range req.Dismissed {
		dismissedRows = append(dismissedRows, []string{f.ID, fmt.Sprintf("%s:%d", f.File, f.Line), f.Title})
	}
	blocks = append(blocks, axi.Table("dismissed", []string{"id", "file:line", "title"}, dismissedRows))
	var oracleRows [][]string
	for _, o := range req.OraclesPassed {
		oracleRows = append(oracleRows, []string{o.Oracle, o.Workspace, o.ReusedFrom})
	}
	blocks = append(blocks, axi.Table("oracles_passed", []string{"oracle", "workspace", "reused_from"}, oracleRows))
	return blocks
}

// staleText says that the branch moved since the round opened, and in which
// repos, with what to do.
func staleText(p verifydeliver.Prepared, moved []verifydeliver.RepoTip) string {
	if len(p.Repos) == 0 {
		return fmt.Sprintf("branch %s moved to %s since this round opened; run `jig gate %s` to review it as it is now", p.Branch, shortSHA(moved[0].Tip), p.Ticket)
	}
	var where []string
	for _, m := range moved {
		where = append(where, fmt.Sprintf("repo %s to %s", m.Repo, shortSHA(m.Tip)))
	}
	return fmt.Sprintf("branch %s moved in %s since this round opened; run `jig gate %s` to review it as it is now", p.Branch, strings.Join(where, ", "), p.Ticket)
}

// reviewTicket is the ticket a review verb is about: the positional when one
// is given, else the ticket the branch the command runs on is for (locate).
func reviewTicket(e env, st *store.Store, jigHome string, positionals []string, verb string, stdout io.Writer) (string, error) {
	arg, err := atMostOne(positionals, verb, "ticket")
	if err != nil {
		return "", err
	}
	if arg != "" {
		return resolveTicketArg(st, arg, stdout)
	}
	cwd, err := e.getwd()
	if err != nil {
		return "", err
	}
	where, err := locate.Locate(jigHome, cwd, locate.Opts{StoreFlag: st.Root, Getenv: e.getenv, NoMap: true})
	if err != nil {
		return "", err
	}
	if where.Ticket == "" {
		return "", &axi.Error{
			Msg:  fmt.Sprintf("cannot tell which ticket this is: the branch %q names none", where.Branch),
			Code: "VALIDATION_ERROR",
			Help: []string{fmt.Sprintf("Name the ticket: `jig %s T-1`", verb)},
		}
	}
	return where.Ticket, nil
}

// reviewerFromFlags is who the verb says the reviewer is: the --session flag,
// else the session the environment names (envSession), and how jig knows.
func reviewerFromFlags(e env, session string) verifydeliver.Provenance {
	if session != "" {
		return verifydeliver.Provenance{Session: session, How: verifydeliver.ReviewerByFlag}
	}
	if env := envSession(e); env != "" {
		return verifydeliver.Provenance{Session: env, How: verifydeliver.ReviewerByEnv}
	}
	return verifydeliver.Provenance{}
}

// cmdReviewContext implements `jig review context [T] [--session ID] [--model
// M] [--effort E] [--json]`: the reviewer's way into a round. It opens the
// round when none is open (the same as `jig gate T`, with its defaults) or
// prints the open one again, notes who the reviewer says it is, and says how to
// report. The session it is run under is how a round learns who reviewed it,
// so it is run by the reviewer.
func cmdReviewContext(e env, args []string, stdout io.Writer) int {
	fs := newFlagSet(e, "review context")
	session := fs.String("session", "", reviewSessionUsage)
	model := fs.String("model", "", reviewModelUsage)
	effort := fs.String("effort", "", reviewEffortUsage)
	asJSON := fs.Bool("json", false, reviewJSONUsage)
	storeFlag := fs.String("store", "", reviewStoreUsage)
	projectFlag := fs.String("project", "", reviewProjectUsage)
	positionals, handled, err := parseWithPositionals(stdout, fs, args)
	if handled {
		return 0
	}
	if err != nil {
		return renderErr(stdout, err)
	}
	if *storeFlag, err = e.abs(*storeFlag); err != nil {
		return renderErr(stdout, err)
	}
	st, cfg, mp, jigHome, err := resolveStoreForProject(e, *projectFlag, *storeFlag, stdout)
	if err != nil {
		return renderErr(stdout, err)
	}
	ticket, err := reviewTicket(e, st, jigHome, positionals, "review context", stdout)
	if err != nil {
		return renderErr(stdout, err)
	}
	if err := requireOpen(st, ticket); err != nil {
		return renderErr(stdout, err)
	}
	if err := requireWork(st, ticket); err != nil {
		return renderErr(stdout, err)
	}

	deps := verifydeliverDeps(e, st, cfg, mp, jigHome)
	p, open, err := verifydeliver.CurrentRound(deps, ticket)
	if err != nil {
		return renderErr(stdout, err)
	}
	if !open {
		if p, err = verifydeliver.GatePrepare(deps, verifydeliver.GateOpts{Ticket: ticket}); err != nil {
			return renderErr(stdout, err)
		}
	}
	who := reviewerFromFlags(e, *session)
	who.Model, who.Effort = *model, *effort
	if who.Known() || who.Model != "" || who.Effort != "" {
		if p, err = verifydeliver.RecordReviewer(deps, p, who); err != nil {
			return renderErr(stdout, err)
		}
	}
	moved, err := verifydeliver.MovedRepos(deps, p)
	if err != nil {
		return renderErr(stdout, err)
	}

	if *asJSON {
		data, err := verifydeliver.MarshalReviewRequest(p.Request)
		if err != nil {
			return renderErr(stdout, err)
		}
		fmt.Fprintf(stdout, "%s\n", data)
		return 0
	}
	blocks := roundBlocks(p, moved, true)
	help := []string{
		"Read the code in the worktree at the head, never in the clone, and edit nothing; run no tests (the oracles above passed)",
		fmt.Sprintf("Report each problem: `jig finding %s --file F --line N --title T --action fix|ask|note --risk low|medium|high --risk-rationale R --oracle O`; confirm an earlier one with `jig finding %s --still-present ID --line N`", ticket, ticket),
		fmt.Sprintf("Then close the round: `jig review done %s --reviewed <every file you read> --summary <text>`", ticket),
	}
	if len(p.Repos) > 0 {
		help = append([]string{help[0], fmt.Sprintf("This round covers the repos %s: read each in its own worktree (the repos table), and name every file as <repo>/<path>, as the must_review paths do - in --file, in --reviewed and in a result file", repoNames(p.Repos))}, help[1:]...)
	}
	blocks = append(blocks, axi.Help(help...))
	axi.Render(stdout, blocks...)
	return 0
}

// repoNames lists the names of the repos of a round, for a sentence.
func repoNames(repos []verifydeliver.PreparedRepo) string {
	names := make([]string, len(repos))
	for i, r := range repos {
		names[i] = r.Repo
	}
	return strings.Join(names, ", ")
}

// stringList is a flag.Value that collects a repeated flag and splits each
// value on commas.
type stringList []string

func (l *stringList) String() string { return strings.Join(*l, ",") }

func (l *stringList) Set(v string) error {
	for _, part := range strings.Split(v, ",") {
		if part = strings.TrimSpace(part); part != "" {
			*l = append(*l, part)
		}
	}
	return nil
}

// cmdReviewDone implements `jig review done [T] (--result FILE | --reviewed
// PATHS --summary TEXT) [--session ID]`: the reviewer's report becomes the
// round. The report is the result file, or the findings drafted with `jig
// finding` and the paths the reviewer read; either is held to the same strict
// parse and checks, so a report that fails any leaves the round open for the
// reviewer to correct. A report from a reviewer that built part of the change
// is recorded but is not clean (REVIEW_NOT_INDEPENDENT).
func cmdReviewDone(e env, args []string, stdout io.Writer) int {
	fs := newFlagSet(e, "review done")
	result := fs.String("result", "", reviewResultUsage)
	var reviewed stringList
	fs.Var(&reviewed, "reviewed", reviewReviewedUsage)
	summary := fs.String("summary", "", reviewSummaryUsage)
	session := fs.String("session", "", reviewSessionUsage)
	storeFlag := fs.String("store", "", reviewStoreUsage)
	projectFlag := fs.String("project", "", reviewProjectUsage)
	positionals, handled, err := parseWithPositionals(stdout, fs, args)
	if handled {
		return 0
	}
	if err != nil {
		return renderErr(stdout, err)
	}
	if *result, err = e.abs(*result); err != nil {
		return renderErr(stdout, err)
	}
	if *storeFlag, err = e.abs(*storeFlag); err != nil {
		return renderErr(stdout, err)
	}
	st, cfg, mp, jigHome, err := resolveStoreForProject(e, *projectFlag, *storeFlag, stdout)
	if err != nil {
		return renderErr(stdout, err)
	}
	ticket, err := reviewTicket(e, st, jigHome, positionals, "review done", stdout)
	if err != nil {
		return renderErr(stdout, err)
	}

	deps := verifydeliverDeps(e, st, cfg, mp, jigHome)
	p, open, err := verifydeliver.CurrentRound(deps, ticket)
	if err != nil {
		return renderErr(stdout, err)
	}
	if !open {
		return renderErr(stdout, &axi.Error{
			Msg:  fmt.Sprintf("no review round is open for %s", ticket),
			Code: "REVIEW_NOT_OPEN",
			Help: []string{fmt.Sprintf("Open one with `jig gate %s` (or `jig review context %s`)", ticket, ticket)},
		})
	}

	drafts, err := verifydeliver.ReadDrafts(p)
	if err != nil {
		return renderErr(stdout, err)
	}
	var rr verifydeliver.ReviewResult
	if *result != "" {
		if len(drafts) > 0 || len(reviewed) > 0 || *summary != "" {
			return renderErr(stdout, &axi.Error{
				Msg:  fmt.Sprintf("--result is the whole report, and %s has findings drafted or paths given already", ticket),
				Code: "VALIDATION_ERROR",
				Help: []string{"Report with --result alone, or with `jig finding` and --reviewed/--summary, not both"},
			})
		}
		data, err := os.ReadFile(*result)
		if err != nil {
			return renderErr(stdout, &axi.Error{Msg: fmt.Sprintf("the result file is unreadable: %v", err), Code: "VALIDATION_ERROR"})
		}
		if rr, err = verifydeliver.ParseReviewResult(data); err != nil {
			return renderErr(stdout, err)
		}
	} else {
		rr = verifydeliver.DraftResult(drafts, reviewed, *summary)
	}

	report, err := verifydeliver.GateFinish(deps, p, rr, reviewerFromFlags(e, *session))
	if report.Round == 0 {
		if err == nil {
			err = &axi.Error{Msg: "the round was not recorded", Code: "REVIEW_FAILED"}
		}
		return renderErr(stdout, err)
	}
	code := printGateReport(stdout, st, ticket, report)
	if err != nil {
		return renderErr(stdout, err)
	}
	return code
}
