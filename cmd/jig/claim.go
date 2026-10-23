package main

import (
	"errors"
	"fmt"
	"io"
	"os"
	"strings"
	"time"

	"github.com/develdeco/jig/internal/axi"
	"github.com/develdeco/jig/internal/gitx"
	"github.com/develdeco/jig/internal/journal"
	"github.com/develdeco/jig/internal/project"
	"github.com/develdeco/jig/internal/repohost"
	"github.com/develdeco/jig/internal/store"
)

// init registers this file's verb with the registry (registry.go).
func init() {
	registerVerb(verb{
		Spec: cmdSpec{"claim", "claim a slice to build it, or let it go", []flagSpec{
			{"slice", sliceFlagUsage, false},
			{"unattended", "no person is watching: a condition the claim does not meet refuses it instead of warning", false},
			{"take", "take the slice over from another session that holds it, and the ticket from another writer that drives it", false},
			{"release", "let the slice go: queued again unless green", false},
			{"session", sessionFlagUsage, false},
			{"store", "explicit store path", false},
			{"project", "project name, resolved via the machine mapping", false},
		}, false},
		Order: 310,
		Run: func(e env, args []string, stdout io.Writer, _ io.Reader) int {
			return cmdClaim(e, args, stdout)
		},
	})
}

// cmdClaim implements `jig claim [<ticket>] [--slice s] [--unattended]
// [--take] [--release] [--session id]`. A claim checks the ticket (blockers
// published and merged, stacked parents clean, the brief approved), takes the
// ticket driver (one writer drives a ticket; another writer's drive refuses
// the claim with DRIVER_HELD unless --take), makes the slice building,
// records the claim line, and the start sha of the repo when the ticket has
// none there yet. Unattended, a condition it does not meet refuses the claim;
// with a person watching it only warns.
func cmdClaim(e env, args []string, stdout io.Writer) int {
	fs := newFlagSet(e, "claim")
	unattended := fs.Bool("unattended", false, "no person is watching: a condition the claim does not meet refuses it instead of warning")
	take := fs.Bool("take", false, "take the slice over from another session that holds it, and the ticket from another writer that drives it")
	release := fs.Bool("release", false, "let the slice go: queued again unless green")
	flags := addWorkFlags(fs, true, true)
	positionals, handled, err := parseWithPositionals(stdout, fs, args)
	if handled {
		return 0
	} else if err != nil {
		return renderErr(stdout, err)
	}
	ticketArg, err := atMostOne(positionals, "jig claim", "ticket")
	if err != nil {
		return renderErr(stdout, err)
	}
	w, err := resolveWork(e, stdout, flags, ticketArg)
	if err != nil {
		return renderErr(stdout, err)
	}
	sl, err := w.slice()
	if err != nil {
		return renderErr(stdout, err)
	}
	if err := w.St.Sync(); err != nil {
		return renderErr(stdout, err)
	}

	if *release {
		return releaseSlice(e, stdout, w, sl)
	}
	isUnattended := *unattended
	if !isUnattended {
		if cwd, err := e.getwd(); err == nil {
			if _, m, ok, err := project.FindMarker(cwd); err == nil && ok && m.Unattended {
				isUnattended = true
			}
		}
	}
	return claimSlice(e, stdout, w, sl, isUnattended, *take)
}

// claimSlice makes the claim: the conditions, the holder, the state, the
// line, the start sha, the push.
func claimSlice(e env, stdout io.Writer, w work, sl store.Slice, unattended, take bool) int {
	ss, err := w.St.ReadSliceState(w.Ticket, sl.ID)
	if err != nil {
		return renderErr(stdout, err)
	}
	if ss.State == "green" {
		return renderErr(stdout, &axi.Error{
			Msg:  fmt.Sprintf("slice %s of %s is green: it is built and verified", sl.ID, w.Ticket),
			Code: "SLICE_GREEN",
			Help: []string{fmt.Sprintf("`jig next %s` shows what is ready to build", w.Ticket)},
		})
	}
	lines, err := journal.Read(w.St, w.Ticket)
	if err != nil {
		return renderErr(stdout, err)
	}
	holder, held := claimHolder(lines, sl.ID)
	mine := held && holder == w.Session
	if held && !mine && !take {
		who := holder
		if who == "" {
			who = "a session with no id"
		}
		return renderErr(stdout, &axi.Error{
			Msg:  fmt.Sprintf("slice %s of %s is held by %s", sl.ID, w.Ticket, who),
			Code: "SLICE_HELD",
			Help: []string{fmt.Sprintf("If that session is gone, take it over: `jig claim %s --slice %s --take`", w.Ticket, sl.ID)},
		})
	}

	deps := claimDeps{merged: func(url string) (bool, bool) {
		gh, _ := e.lookPath("gh")
		return repohost.PRMerged(gh, nil, url)
	}}
	problems, err := claimProblems(w.St, w.Ticket, deps)
	if err != nil {
		return renderErr(stdout, err)
	}
	if len(problems) > 0 && unattended {
		return renderErr(stdout, refusal(w, sl, problems))
	}
	if err := takeDriver(w, sl, take); err != nil {
		return renderErr(stdout, err)
	}

	result, tookFrom := "already-claimed", ""
	if !mine {
		result = "claimed"
		if held {
			result, tookFrom = "taken-over", holder
			if tookFrom == "" {
				tookFrom = "a session with no id"
			}
		}
	}
	started, err := recordClaim(e, w, sl, ss, unattended, !mine)
	if err != nil {
		return renderErr(stdout, err)
	}
	return printClaim(stdout, w, sl, result, started, problems, tookFrom)
}

// takeDriver makes the writer of this clone the driver of the ticket, for the
// session claiming (store.ClaimDriver): a no-op when it drives it already,
// the DRIVER_HELD refusal when another writer does and take is not set. The
// refusal is given the exact command that takes the ticket over.
func takeDriver(w work, sl store.Slice, take bool) error {
	err := w.St.ClaimDriver(w.Ticket, w.Session, take)
	var ae *axi.Error
	if errors.As(err, &ae) && ae.Code == store.DriverHeldCode {
		ae.Help = []string{fmt.Sprintf("If they are not working on it any more, take it over: `jig claim %s --slice %s --take`", w.Ticket, sl.ID)}
	}
	return err
}

// releaseDriverIfIdle ends the drive of the ticket by this writer once no
// slice of it is held any more: lines are the ticket journal, release line
// included. Only the claims made since the writer became the driver count,
// the claim lines of a driver it took the ticket over from do not: they name
// a session of someone else, and a claim of theirs that was never released
// would otherwise keep the ticket driven for good. A ticket this writer does
// not drive has nothing to release.
func releaseDriverIfIdle(w work, lines []journal.Line) error {
	me, err := w.St.Writer()
	if err != nil {
		return err
	}
	d, driven, err := w.St.Driver(w.Ticket)
	if err != nil || !driven || d.Writer != me {
		return err
	}
	since, perr := time.Parse(time.RFC3339Nano, d.TS)
	var epoch []journal.Line
	for _, l := range lines {
		if at, err := time.Parse(time.RFC3339Nano, l.TS); perr == nil && err == nil && at.Before(since) {
			continue
		}
		epoch = append(epoch, l)
	}
	slices, err := w.St.ReadSlices(w.Ticket)
	if err != nil {
		return err
	}
	for _, sl := range slices {
		if _, held := claimHolder(epoch, sl.ID); held {
			return nil
		}
	}
	return w.St.ReleaseDriver(w.Ticket, w.Session)
}

// recordClaim writes what a claim leaves behind: the slice building, the
// start sha of the repo when the ticket has none there yet, the claim line
// (newLine; a session claiming again what it holds adds none), and a push.
// started is the start sha it recorded, "" when it recorded none.
func recordClaim(e env, w work, sl store.Slice, ss store.SliceState, unattended, newLine bool) (started string, err error) {
	if next := claimedState(ss); next != ss {
		if err := w.St.WriteSliceState(w.Ticket, sl.ID, next); err != nil {
			return "", err
		}
	}
	if started, err = ensureStartSHA(w); err != nil {
		return "", err
	}
	if newLine {
		err = journal.Append(w.St, w.Ticket, journal.Line{
			TS: stamp(e), Slice: sl.ID, Event: journal.EventClaim,
			Session: w.Session, Unattended: unattended, Branch: w.Where.Branch,
		})
		if err != nil {
			return "", err
		}
	}
	return started, w.St.Push(fmt.Sprintf("%s: slice %s claimed", w.Ticket, sl.ID))
}

// stamp is the time on e as a journal line records it.
func stamp(e env) string {
	return e.now().UTC().Format(time.RFC3339Nano)
}

// ensureStartSHA records where the ticket branch started in the repo the work
// is in, unless the ticket has that already: the merge base of HEAD and the
// target branch of the repo. It returns the sha it recorded, "" when it
// recorded none (it was there, or the repo has no target to measure from).
func ensureStartSHA(w work) (string, error) {
	repo, dir := w.Where.Repo, w.buildDir()
	if repo == "" || dir == "" {
		return "", nil
	}
	path := w.St.StartSHAPath(w.Ticket, repo)
	if _, err := os.Stat(path); err == nil {
		return "", nil
	} else if !os.IsNotExist(err) {
		return "", err
	}
	target := "main"
	for _, r := range w.Cfg.Repos {
		if r.Name() == repo {
			target = r.TargetBranch()
		}
	}
	for _, ref := range []string{"refs/remotes/origin/" + target, "refs/heads/" + target} {
		if base, err := gitx.MergeBase(dir, "HEAD", ref); err == nil && base != "" {
			return base, w.St.WriteStartSHA(w.Ticket, repo, base)
		}
	}
	return "", nil
}

// refusal is the error of a claim made unattended that does not meet its
// conditions: the first condition names the code, all of them the message.
func refusal(w work, sl store.Slice, problems []claimProblem) *axi.Error {
	msgs := make([]string, len(problems))
	helps := make([]string, len(problems))
	for i, p := range problems {
		msgs[i], helps[i] = p.Message, p.Help
	}
	return &axi.Error{
		Msg:  fmt.Sprintf("slice %s of %s cannot be claimed unattended: %s", sl.ID, w.Ticket, strings.Join(msgs, "; ")),
		Code: problems[0].Code,
		Help: helps,
	}
}

// printClaim reports a claim: where it stands, the conditions it did not meet
// (a person is watching, so they only warn), and the next step.
func printClaim(stdout io.Writer, w work, sl store.Slice, result, started string, problems []claimProblem, tookFrom string) int {
	ss, err := w.St.ReadSliceState(w.Ticket, sl.ID)
	if err != nil {
		return renderErr(stdout, err)
	}
	pairs := [][2]string{{"ticket", w.Ticket}, {"slice", sl.ID}, {"result", result}, {"state", ss.State}}
	add := func(k, v string) {
		if v != "" {
			pairs = append(pairs, [2]string{k, v})
		}
	}
	add("session", w.Session)
	add("branch", w.Where.Branch)
	add("start", shortSHA(started))
	add("took_over_from", tookFrom)
	blocks := []string{axi.KV("claim", pairs)}
	if len(problems) > 0 {
		rows := make([][]string, len(problems))
		for i, p := range problems {
			rows[i] = []string{p.Code, p.Message, p.Help}
		}
		blocks = append(blocks, axi.Table("warnings", []string{"code", "message", "help"}, rows))
	}
	help := []string{fmt.Sprintf("Run `jig context %s --slice %s` for the brief, what is built and the oracle; commit with the trailer `Jig-Slice: %s`", w.Ticket, sl.ID, sl.ID)}
	if w.Session == "" {
		help = append(help, "No session id: pass --session or set JIG_SESSION, so `jig here` knows which slice you hold")
	}
	blocks = append(blocks, axi.Help(help...))
	axi.Render(stdout, blocks...)
	return 0
}

// releaseSlice lets go of the slice: queued again unless it is green or parked
// on a question, and a release line when anything held it or changed.
func releaseSlice(e env, stdout io.Writer, w work, sl store.Slice) int {
	ss, err := w.St.ReadSliceState(w.Ticket, sl.ID)
	if err != nil {
		return renderErr(stdout, err)
	}
	lines, err := journal.Read(w.St, w.Ticket)
	if err != nil {
		return renderErr(stdout, err)
	}
	_, held := claimHolder(lines, sl.ID)
	next := releasedState(ss)
	result := "released"
	if !held && next == ss {
		result = "not-claimed"
	} else {
		if next != ss {
			if err := w.St.WriteSliceState(w.Ticket, sl.ID, next); err != nil {
				return renderErr(stdout, err)
			}
		}
		err := journal.Append(w.St, w.Ticket, journal.Line{
			TS: stamp(e), Slice: sl.ID, Event: journal.EventRelease, Session: w.Session, Branch: w.Where.Branch,
		})
		if err != nil {
			return renderErr(stdout, err)
		}
		after, err := journal.Read(w.St, w.Ticket)
		if err != nil {
			return renderErr(stdout, err)
		}
		if err := releaseDriverIfIdle(w, after); err != nil {
			return renderErr(stdout, err)
		}
		if err := w.St.Push(fmt.Sprintf("%s: slice %s released", w.Ticket, sl.ID)); err != nil {
			return renderErr(stdout, err)
		}
	}
	axi.Render(stdout,
		axi.KV("claim", [][2]string{{"ticket", w.Ticket}, {"slice", sl.ID}, {"result", result}, {"state", next.State}}),
		axi.Help(fmt.Sprintf("`jig next %s` shows what is ready to build", w.Ticket)),
	)
	return 0
}
