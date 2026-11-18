package verify

import (
	"errors"
	"fmt"
	"io"
	"sort"
	"strings"
	"time"

	"github.com/develdeco/jig/internal/envrun"
	"github.com/develdeco/jig/internal/journal"
	"github.com/develdeco/jig/internal/manifest"
	"github.com/develdeco/jig/internal/ready"
	"github.com/develdeco/jig/internal/store"
)

// FinalRequest names a final run: the union of a ticket's slice commands, run
// in the worktree Deps.Clone and recorded against its HEAD. It is the last run
// of a ticket's own commands, made by the builder at the head a gate round
// reviewed, and it adds no command of its own.
type FinalRequest struct {
	Ticket string
	// Session and Agent are who asked, written on the line the run records.
	Session string
	Agent   string
	// Flags is the command line's flags as given, recorded on the line so the
	// run can be made again by hand.
	Flags string
	// Timeout bounds the command when it is above 0; the run is unbounded
	// otherwise.
	Timeout time.Duration
	// Dir is where the command runs; the worktree's root when empty.
	Dir string
	// NoRecord runs the command and writes nothing.
	NoRecord bool
}

// FinalSlice is one slice of the union and the command it contributes.
type FinalSlice struct {
	ID      string
	Command string
	// Env is the env class the slice names, "" for none.
	Env string
}

// FinalResult is what a final run found: Result, with Slice empty and Command
// the composed command, and the slices the command is the union of.
type FinalResult struct {
	Result
	// Slices lists every slice of the ticket that builds in this repo, in
	// slices.yaml order, each with the command it contributes (a command that
	// an earlier slice already contributed runs once).
	Slices []FinalSlice
	// Classes are the env classes the union brings up, sorted.
	Classes []string
}

// finalRun is a final run in flight: the run's shared state plus the union.
type finalRun struct {
	*run
	slices  []FinalSlice
	classes []string
}

// prepareFinal checks the request against the store and the worktree and
// resolves what the run needs before it touches the journal.
func (d Deps) prepareFinal(r FinalRequest) (*finalRun, error) {
	x := &run{d: d, r: Request{Ticket: r.Ticket, Session: r.Session, Agent: r.Agent, Flags: r.Flags, Timeout: r.Timeout, Dir: r.Dir, NoRecord: r.NoRecord},
		res: Result{Ticket: r.Ticket}}
	f := &finalRun{run: x}
	switch {
	case d.Store == nil:
		return f, errors.New("verify: no store")
	case d.JigHome == "":
		return f, errors.New("verify: no jig home")
	case d.Clone == "":
		return f, errors.New("verify: no worktree to verify")
	case r.Ticket == "":
		return f, errors.New("verify: a final run names a ticket")
	}
	if err := x.prepareHead(); err != nil {
		return f, err
	}
	var err error
	if x.storeID, err = d.Store.ID(); err != nil {
		return f, fmt.Errorf("verify: %w", err)
	}
	return f, nil
}

// mine is the slices of the ticket that build in this worktree's repo: every
// slice of a single-repo project, the slices ready.Place puts here in a
// project of several.
func (d Deps) mine(all []store.Slice) []store.Slice {
	if len(d.Repos) < 2 {
		return all
	}
	place := ready.Place(d.Repos, all)
	var out []store.Slice
	for _, s := range all {
		if place.Repo[s.Workspace] == d.Repo {
			out = append(out, s)
		}
	}
	return out
}

// compose resolves the union at the manifest of HEAD: each slice's command as
// `jig verify --slice` resolves it, the identical ones once at their first
// position, as one sh command line `(a) && (b)`. why is the reason the union
// cannot be reached, "" when it can.
func (f *finalRun) compose(m manifest.Manifest) (composed, why string) {
	x := f.run
	all, err := x.d.Store.ReadSlices(x.r.Ticket)
	if err != nil {
		return "", fmt.Sprintf("read the slices of %s: %v", x.r.Ticket, err)
	}
	mine := x.d.mine(all)
	if len(mine) == 0 {
		return "", fmt.Sprintf("no slice of %s builds in this repo, so there is no command to run", x.r.Ticket)
	}
	seen := map[string]bool{}
	classes := map[string]bool{}
	var parts []string
	f.slices = nil
	for _, s := range mine {
		ws, ok := m.WorkspaceIn(x.d.Repo, s.Workspace)
		if !ok {
			return "", fmt.Sprintf("slice %s builds in the workspace %q, which the manifest at %s does not declare", s.ID, s.Workspace, short(x.sha))
		}
		cmd := strings.TrimSpace(m.OracleCmd(s.Oracle, ws))
		if cmd == "" {
			return "", fmt.Sprintf("slice %s has no oracle to run", s.ID)
		}
		f.slices = append(f.slices, FinalSlice{ID: s.ID, Command: cmd, Env: s.Env})
		if s.Env != "" {
			classes[s.Env] = true
		}
		if !seen[cmd] {
			seen[cmd] = true
			parts = append(parts, "("+cmd+")")
		}
	}
	f.classes = f.classes[:0]
	for c := range classes {
		f.classes = append(f.classes, c)
	}
	sort.Strings(f.classes)
	return strings.Join(parts, " && "), ""
}

// result is the FinalResult of what the run knows now.
func (f *finalRun) result() FinalResult {
	return FinalResult{Result: f.res, Slices: f.slices, Classes: f.classes}
}

// PlanFinal says what RunFinal would do for r, and runs and writes nothing:
// the commit, the union and the slices it comes from, the directory it would
// run in, the tree as it is now, and the reason it would cover nothing when
// that is already so. A union that cannot be reached is an error carrying the
// reason.
func PlanFinal(d Deps, r FinalRequest) (FinalResult, error) {
	f, err := d.prepareFinal(r)
	if err != nil {
		return f.result(), err
	}
	m, err := manifest.Resolve(f.dir)
	if err != nil {
		return f.result(), fmt.Errorf("verify: the manifest at %s does not resolve: %w", short(f.sha), err)
	}
	composed, why := f.compose(m)
	if why != "" {
		return f.result(), fmt.Errorf("verify: %s", why)
	}
	if why := f.checkClasses(m); why != "" {
		return f.result(), fmt.Errorf("verify: %s", why)
	}
	f.res.Command, f.res.Env = composed, strings.Join(f.classes, ",")
	f.res.TreeBefore = treeState(f.dir)
	f.res.HeadEnd = f.sha
	f.res.TreeAfter = f.res.TreeBefore
	f.res.Uncovered = f.uncoveredBecause(true, f.res.TreeBefore, f.sha)
	f.res.Covered = f.res.Uncovered == ""
	return f.result(), nil
}

// RunFinal runs the union of the ticket's slice commands at HEAD of the
// worktree Deps.Clone, in this process, in the foreground and the caller's
// environment, streams its output, and records the run in one final-oracle
// line. The command is one `sh -c` of the commands, each in its own
// parentheses joined by ` && `, so the first failure ends the run and its
// code is the run's. jig adds no command: the union is exactly what the
// slices name.
//
// A final run covers HEAD under a slice run's rules: the tree clean before
// and after, HEAD not moved, the union (never another command), run in the
// worktree, and HEAD descending from the ticket's start sha. It changes no
// slice's state and writes no verified line, no recorded line and no
// recording. A run that cannot reach its command (no sh, a manifest that does
// not resolve, a slice whose workspace the manifest lacks, an env class that
// is undeclared or will not come up) writes nothing and comes back as State
// error with the reason in Summary. RunFinal returns an error only when the
// run could not be recorded or was not set up.
func RunFinal(d Deps, r FinalRequest) (FinalResult, error) {
	f, err := d.prepareFinal(r)
	if err != nil {
		return f.result(), err
	}
	why, err := f.execute()
	if err != nil {
		return f.result(), err
	}
	if why != "" {
		f.res.State, f.res.Summary, f.res.Code = journal.VerifyError, oneLine(why), ExitUnrun
	}
	return f.result(), nil
}

// checkClasses says why the env classes of the union cannot come up: one the
// manifest does not declare.
func (f *finalRun) checkClasses(m manifest.Manifest) string {
	for _, c := range f.classes {
		if _, ok := m.Envs[c]; !ok {
			return fmt.Sprintf("the union names the env class %q, which the manifest at %s does not declare", c, short(f.sha))
		}
	}
	return ""
}

// bringUpEnvs brings the union's env classes up once, in sorted order, each
// with an env-up line, and returns what brings them down again in reverse
// order with their env-down lines. A class that will not come up takes the
// ones already up down again and is a reason the command cannot be reached.
func (f *finalRun) bringUpEnvs(m manifest.Manifest) (down func() error, why string, err error) {
	if len(f.classes) == 0 {
		return nil, "", nil
	}
	if why := f.checkClasses(m); why != "" {
		return nil, why, nil
	}
	x := f.run
	var handles []*envrun.Handle
	var names []string
	downAll := func() error {
		var first error
		for i := len(handles) - 1; i >= 0; i-- {
			_ = handles[i].Down()
			if jerr := x.journal(journal.Line{Event: journal.EventEnvDown, Env: names[i]}); jerr != nil && first == nil {
				first = jerr
			}
		}
		return first
	}
	for _, c := range f.classes {
		h, uerr := envrun.Up(m.Envs[c], x.r.Ticket, x.cmdDir)
		if uerr != nil {
			_ = downAll()
			var unavail *envrun.Unavailable
			if errors.As(uerr, &unavail) {
				return nil, fmt.Sprintf("the env class %q is unavailable: %v", c, unavail.Err), nil
			}
			return nil, fmt.Sprintf("bring up the env class %q: %v", c, uerr), nil
		}
		handles, names = append(handles, h), append(names, c)
		if jerr := x.journal(journal.Line{Event: journal.EventEnvUp, Env: c}); jerr != nil {
			_ = downAll()
			return nil, "", jerr
		}
	}
	return downAll, "", nil
}

// execute does the run: the union resolved, the env classes up, the command,
// the record. It returns why the command could not be reached ("" when it
// ran), or an error when the line could not be written.
func (f *finalRun) execute() (why string, err error) {
	x := f.run
	if _, err := x.d.sh(); err != nil {
		return err.Error(), nil
	}
	m, err := manifest.Resolve(x.dir)
	if err != nil {
		return fmt.Sprintf("the manifest at %s does not resolve: %v", short(x.sha), err), nil
	}
	cmd, why := f.compose(m)
	if why != "" {
		return why, nil
	}
	x.res.Command, x.res.Env = cmd, strings.Join(f.classes, ",")
	x.res.TreeBefore = treeState(x.dir)
	// What already says the run cannot cover is settled before the env classes
	// come up, as a slice run's is.
	x.pre = x.uncoveredBecause(true, x.res.TreeBefore, x.sha)

	down, why, err := f.bringUpEnvs(m)
	if why != "" || err != nil {
		return why, err
	}
	if down != nil {
		defer func() {
			if derr := down(); derr != nil && err == nil {
				err = derr
			}
		}()
	}
	if err := f.runUnion(cmd); errors.Is(err, envrun.ErrStopped) {
		// jig was stopped mid-run: no verdict, and never the commit's failure.
		return "", nil
	} else if err != nil {
		return "", err
	}
	return "", nil
}

// runUnion runs the composed command in the worktree and records what came of
// it: one final-oracle line, and nothing else.
func (f *finalRun) runUnion(cmd string) error {
	x := f.run
	d := x.d
	ids := make([]string, len(f.slices))
	for i, s := range f.slices {
		ids[i] = s.ID
	}
	base := func() journal.Line {
		l := journal.Line{Event: journal.EventFinalOracle, Commit: x.sha, Command: cmd, Env: x.res.Env, Source: "jig",
			Dir: x.cmdDir, Subject: x.subject, Flags: x.r.Flags, HeadEnd: x.headNow(), TreeBefore: x.res.TreeBefore, TreeAfter: treeState(x.dir),
			Slices: ids}
		if len(d.Repos) > 1 {
			l.Repo = d.Repo
		}
		return l
	}
	tail := &tailWriter{}
	w := io.Writer(tail)
	if d.Out != nil {
		w = io.MultiWriter(d.Out, tail)
	}
	started := d.now()
	x.res.Ran = true
	use, oerr := d.oracle(cmd, x.cmdDir, nil, x.r.Timeout, w)
	if errors.Is(oerr, envrun.ErrStopped) {
		line := base()
		line.Outcome, line.Cause, line.Summary = "fail", journal.CauseStopped, oneLine(oerr.Error())
		if jerr := x.journal(line); jerr != nil {
			return jerr
		}
		x.recordedLine(journal.EventFinalOracle)
		return oerr
	}
	seconds := int((d.now().Sub(started) + time.Second - 1) / time.Second)
	if seconds < 1 {
		seconds = 1
	}
	x.res.Seconds = seconds
	x.res.Code = exitCodeOf(oerr)

	line := base()
	line.Seconds = seconds
	x.res.HeadEnd, x.res.TreeAfter = line.HeadEnd, line.TreeAfter
	line.SetUsage(use.CPU, use.PeakBytes, use.Basis)
	line.Uncovered = x.uncoveredBecause(true, line.TreeAfter, line.HeadEnd)
	x.res.Uncovered, x.res.Covered = line.Uncovered, line.Uncovered == ""
	if oerr == nil {
		line.Outcome = "pass"
		x.res.State = journal.VerifyPass
	} else {
		x.res.Tail, x.res.Summary = failureOutput(tail.String(), oerr)
		x.res.Signature = failureSignature("final", x.res.Summary, oerr)
		x.res.State = journal.VerifyFail
		line.Outcome, line.Summary, line.Signature = "fail", x.res.Summary, x.res.Signature
		line.Cause = failureCause(oerr)
	}
	if err := x.journal(line); err != nil {
		return err
	}
	x.recordedLine(journal.EventFinalOracle)
	// Recorded, and nothing more: a final run is evidence about the union, so
	// it marks no slice green and writes no verified line.
	x.push("final run recorded")
	return nil
}
