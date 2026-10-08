package verifydeliver

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"

	"github.com/develdeco/jig/internal/axi"
	"github.com/develdeco/jig/internal/gitx"
	"github.com/develdeco/jig/internal/media"
	"github.com/develdeco/jig/internal/session"
	"github.com/develdeco/jig/internal/store"
)

// DemoInput is what Gate hands a DemoSource for one demo: the round's own
// coordinates, the gate lease at the reviewed head, and where the media go.
type DemoInput struct {
	Store    *store.Store
	Ticket   string
	Round    int
	LeaseDir string
	Model    string
	Intent   Intent
	BaseSHA  string
	HeadSHA  string
	MediaDir string
}

// DemoSource is a GateSource that can also dispatch a demo session after a
// clean round. Only the reviewer source implements it: a scripted round
// never runs a demo, structurally, because its source has no Demo method
// for Gate to find.
type DemoSource interface {
	Demo(in DemoInput) (DemoResult, error)
}

// Demo writes demo.json beside the media directory, dispatches a demo session
// in the gate lease, and returns its result strictly parsed. Every failure is
// an error the caller records as a refusal (it treats a demo as best effort,
// never as part of the round): a failure of the session or its backend, or a
// session that wrote no demo result, is a demoFailure, which is recorded as a
// code alone; any other error is jig's own refusal, whose text is its reason
// (gateDemo leaves the host paths out of it). The lease is checked with the
// reviewer's own read-only guard, and restored pristine afterwards whatever
// happened, so a demo never leaves the lease for a later operation to trip
// over. The files a session wrote into media_dir are the caller's to verify.
func (r *reviewerGateSource) Demo(in DemoInput) (res DemoResult, err error) {
	head, err := gitx.RevParse(in.LeaseDir, "HEAD")
	if err != nil {
		return DemoResult{}, fmt.Errorf("resolve the gate lease's HEAD: %w", err)
	}
	if head != in.HeadSHA {
		return DemoResult{}, fmt.Errorf("the gate lease is at %s, not the reviewed head %s", head, in.HeadSHA)
	}
	// The restore targets the captured sha, never the literal ref "HEAD",
	// for the same reason the reviewer round's does: a session that
	// committed moved HEAD itself.
	defer func() {
		if rerr := resetLeasePristine(in.LeaseDir, head); rerr != nil && err == nil {
			err = fmt.Errorf("restore the gate lease after the demo: %w", rerr)
			res = DemoResult{}
		}
	}()

	req := DemoRequest{
		Ticket:   in.Ticket,
		Round:    in.Round,
		BaseSHA:  in.BaseSHA,
		HeadSHA:  in.HeadSHA,
		Intent:   in.Intent,
		MediaDir: absPath(in.MediaDir),
		Limits:   demoLimits(),
	}
	reqData, err := json.MarshalIndent(req, "", "  ")
	if err != nil {
		return DemoResult{}, fmt.Errorf("marshal demo.json: %w", err)
	}
	// demo.json names media_dir by its host path, so it sits beside the media
	// under the jig home; only the result, the session's own words, goes to
	// the store.
	demoPath := demoJSONPath(req.MediaDir)
	resultPath := demoResultJSONPath(in.Store, in.Ticket, in.Round)
	if err := os.MkdirAll(filepath.Dir(resultPath), 0o755); err != nil {
		return DemoResult{}, fmt.Errorf("create the work dir: %w", err)
	}
	if err := os.WriteFile(demoPath, reqData, 0o644); err != nil {
		return DemoResult{}, fmt.Errorf("write demo.json: %w", err)
	}
	// A failed earlier attempt at this round must never be read back as
	// this attempt's result.
	if err := os.Remove(resultPath); err != nil && !os.IsNotExist(err) {
		return DemoResult{}, fmt.Errorf("remove a stale demo.result.json: %w", err)
	}

	dispatch := session.Dispatch{
		Ticket:        in.Ticket,
		Slice:         session.GateDemoSlice,
		Attempt:       in.Round,
		Worktree:      in.LeaseDir,
		SliceJSON:     demoPath,
		ResultJSON:    resultPath,
		ExtraWriteDir: req.MediaDir,
		Model:         in.Model,
		Prompt:        RenderDemoPrompt(req, demoPath, resultPath),
		Screen:        true,
	}
	if err := r.backend.Run(dispatch); err != nil {
		return DemoResult{}, demoFailed(resultPath, err)
	}
	resultData, err := os.ReadFile(resultPath)
	if err != nil {
		return DemoResult{}, demoFailed(resultPath, noDemoResult(fmt.Sprintf("it wrote no demo result (%v)", err)))
	}
	// A backend writes a result of its own in place of a session's when the
	// session wrote none, and that file has the slice result's shape. It is
	// not a demo result that failed the contract: the session wrote no demo
	// result, and the file is the backend's, so it is not kept.
	if outcome, summary, ok := backendFallback(resultData); ok {
		return DemoResult{}, demoFailed(resultPath, noDemoResult(
			fmt.Sprintf("it wrote no demo result, and the backend recorded its own outcome in its place (%s: %s)", outcome, summary)))
	}

	changed, err := leaseChanged(in.LeaseDir, head)
	if err != nil {
		return DemoResult{}, fmt.Errorf("check the gate lease after the demo session: %w", err)
	}
	if changed {
		return DemoResult{}, fmt.Errorf("the demo session changed the gate lease; a demo never edits tracked files")
	}
	return ParseDemoResult(resultData)
}

// noDemoResult is the cause of a demo session that wrote no demo result: its
// code is what demo.yaml records for it.
func noDemoResult(msg string) error {
	return &axi.Error{Msg: msg, Code: "DEMO_NO_RESULT"}
}

// demoFailed returns cause as a demoFailure, after removing the file at
// resultPath. A result file that is there after a dispatch failed is the
// backend's, not a demo result, and it is in the store's work directory, which
// the demo's own store push commits: the store carries no text of the backend's or
// the session's. A file that cannot be removed is said in the failure's text.
func demoFailed(resultPath string, cause error) error {
	if err := os.Remove(resultPath); err != nil && !os.IsNotExist(err) {
		cause = errors.Join(cause, fmt.Errorf("the backend's result file could not be removed: %w", err))
	}
	return &demoFailure{err: cause}
}

// gateDemo runs one round's demo, after the round's own store writes are
// committed and pushed, and records it. It is best effort by construction:
// it returns no error, every failure becomes a refused demo.yaml and a
// report, and the round's verdict is never touched. A round whose head
// already has a recorded demo runs none.
func gateDemo(d Deps, ds DemoSource, in DemoInput, repoName, target string) DemoReport {
	prior, err := recordedDemoRound(d.Store, in.Ticket, in.Round, in.HeadSHA)
	if prior != 0 {
		return DemoReport{Status: DemoExisting, Round: prior}
	}
	// A demo.yaml that cannot be read (err set, prior 0) is a refusal like any
	// other: it is recorded for this round, and no session is dispatched on top
	// of it.
	var (
		files    []DemoFile
		summary  string
		mediaDir string
	)
	if err == nil {
		mediaDir, err = demoMediaDir(d, in.Ticket, in.HeadSHA)
	}
	if err == nil {
		files, summary, err = attemptDemo(d, ds, in, mediaDir, repoName, target)
	}
	// A refusal's reason is committed to the store and printed, and the errors
	// behind it name the paths they failed on: name the directories by role
	// instead. (A failure of the session or its backend records a code only.)
	hide := func(s string) string {
		return leaveOutHostPaths(s,
			hostDir{mediaDir, "media_dir"}, hostDir{d.Home, "<jig home>"}, hostDir{d.Store.Root, "<store>"})
	}

	var refusal *demoRefusal
	if err != nil {
		r := refusalOf(err, hide)
		refusal = &r
	}
	report, werr := writeDemoRecord(d.Store, in.Ticket, in.Round, in.HeadSHA, files, summary, refusal)
	if werr != nil {
		return DemoReport{Status: DemoRefused, Reason: demoReason(errors.New(hide(fmt.Sprintf("demo.yaml could not be recorded: %v", werr))))}
	}
	if perr := d.Store.Push(fmt.Sprintf("%s: gate round %d demo %s", in.Ticket, in.Round, report.Status)); perr != nil {
		report.Warning = fmt.Sprintf("the store push carrying demo.yaml failed, so it reaches the remote with the next store push: %v", perr)
	}
	return report
}

// attemptDemo prepares an empty dir (the head's media_dir) and notes which
// directory it is, dispatches the demo through ds, verifies what the session
// wrote, renames the accepted files, and removes everything else from it, so
// the directory holds exactly what demo.yaml will list. Any error refuses the
// whole demo.
func attemptDemo(d Deps, ds DemoSource, in DemoInput, dir, repoName, target string) ([]DemoFile, string, error) {
	base, err := resolveFullBase(d.Store, in.Ticket, in.LeaseDir, repoName, target, in.HeadSHA)
	if err != nil {
		return nil, "", err
	}
	in.BaseSHA = base
	in.MediaDir = dir
	// The store-id and ticket directories between the jig home's evidence
	// directory and the head's must be plain.
	if err := media.PlainParents(absPath(filepath.Join(d.Home, "evidence")), dir); err != nil {
		return nil, "", err
	}
	// A refused or failed earlier attempt at this head may have left files
	// behind, and a session's own files must never mix with them. RemoveAll
	// does not follow a link, so a link a session left in the directory is
	// removed as itself.
	if err := os.RemoveAll(dir); err != nil {
		return nil, "", fmt.Errorf("clear media_dir: %w", err)
	}
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return nil, "", fmt.Errorf("create media_dir: %w", err)
	}
	// The directory as it is now, for verifyDemoMedia to compare: a plain
	// directory check alone would follow a directory above it that the session
	// swapped for a link.
	made, err := media.LstatPinned(dir)
	if err != nil {
		return nil, "", fmt.Errorf("read media_dir: %w", err)
	}

	res, err := ds.Demo(in)
	if err != nil {
		return nil, "", err
	}
	accepted, err := verifyDemoMedia(dir, made, res)
	if err != nil {
		return nil, "", err
	}
	files, err := renameDemoMedia(dir, accepted)
	if err != nil {
		return nil, "", err
	}
	if err := pruneDemoMedia(dir, files); err != nil {
		return nil, "", err
	}
	return files, res.Summary, nil
}
