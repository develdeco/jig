package verifydeliver

// The publish step that picks the build's recordings (ADR 0029, step 5): the
// candidates from the journal, a reused or a freshly dispatched pick, its
// validation, the staging of the picked files in a directory of jig's own, one
// journal line, and the rendered section writePRBody uses in place of the gate
// demo's. picks.go holds the pieces; this file runs them in order.

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"

	"github.com/develdeco/jig/internal/axi"
	"github.com/develdeco/jig/internal/home"
	"github.com/develdeco/jig/internal/journal"
	"github.com/develdeco/jig/internal/media"
	"github.com/develdeco/jig/internal/pool"
	"github.com/develdeco/jig/internal/session"
	"github.com/develdeco/jig/internal/staircase"
)

// A publish's pick, as PicksReport.Status says it: made now, taken from an
// earlier publish of the same head and the same recordings, or refused (and
// the gate's demo stands in). "" means there was nothing to pick from.
const (
	PicksPicked  = "picked"
	PicksReused  = "reused"
	PicksRefused = "refused"
)

// picksEffort is the reasoning effort of a pick session: choosing among
// captions is the cheapest work a session does for jig.
const picksEffort = "low"

// PicksReport is what a publish says of its pick. The zero value is a publish
// with no recordings to pick from, which behaves as it always has.
type PicksReport struct {
	Status string
	// Reason is why a pick was refused, one line in jig's words.
	Reason string
	// Flows and Files count what a pick (made or reused) shows.
	Flows, Files int
	// Dropped names the candidate recordings that failed their check, with why.
	Dropped []string
}

// pickError is a pick that did not stand: reason is what the journal and the
// report record, in jig's words; detail, when set, is a session failure's own
// text, printed once and never recorded.
type pickError struct{ reason, detail string }

func (e *pickError) Error() string { return e.reason }

func pickRefused(format string, args ...any) error {
	return &pickError{reason: fmt.Sprintf(format, args...)}
}

// pickFailed is a pick whose session, or the backend that ran it, failed. As
// with a failed demo, the record carries the failure's code alone: the text of
// a session's or a backend's error can hold anything.
func pickFailed(cause error) error {
	return &pickError{reason: "the pick session failed: " + failureCode(cause), detail: demoOneLine(cause.Error())}
}

// picksStep is everything one publish's pick needs. head is the head publish
// ships before it reconciles, the one the gate reviewed; leaseDir is the
// publish lease, where the recorded commits are looked up.
type picksStep struct {
	d        Deps
	backend  session.Backend
	ticket   string
	repoName string
	leaseDir string
	head     string
	lines    []journal.Line
	warn     func(format string, args ...any)
}

// publishPicks runs the pick for one publish. With no candidate recordings it
// does nothing and returns the zero report, so a ticket whose build recorded
// nothing publishes exactly as before. Otherwise it returns the report and,
// when a pick stands, the rendered section for writePRBody. A pick that fails
// or is refused is never an error here: it is journaled, said in the report
// and the output, and writePRBody then renders the gate's demo instead. The
// error is publish's own failure, the journal line that could not be written.
func publishPicks(s picksStep) (PicksReport, *DemoRenderResult, error) {
	cands, dropped, cerr := pickCandidates(s.d, s.ticket, s.lines, s.leaseDir, s.head)
	report := PicksReport{Dropped: dropped}
	if len(dropped) > 0 {
		s.warn("jig: recordings left out of the choice for %s: %s\n", s.ticket, strings.Join(dropped, "; "))
	}
	if cerr == nil && len(cands) == 0 {
		return report, nil, nil
	}

	dir, hide := s.hider()
	refuse := func(err error) (PicksReport, *DemoRenderResult, error) {
		var pe *pickError
		if !errors.As(err, &pe) {
			pe = &pickError{reason: err.Error()}
		}
		report.Status, report.Reason = PicksRefused, demoReason(errors.New(hide(pe.reason)))
		msg := report.Reason
		if pe.detail != "" {
			msg += " (" + pe.detail + ")"
		}
		s.warn("jig: no recordings were picked for %s: %s\n", s.ticket, msg)
		if err := journal.Append(s.d.Store, s.ticket, journal.Line{Event: "publish-picks", Commit: s.head, Outcome: PicksRefused + ": " + report.Reason}); err != nil {
			return report, nil, fmt.Errorf("verifydeliver: publish: journal publish-picks: %w", err)
		}
		return report, nil, nil
	}
	if cerr != nil {
		return refuse(pickRefused("the recordings could not be listed: %v", cerr))
	}

	// The intent the pick is judged against is part of the question: resolved
	// once, here, it is what the session is handed and what a reused pick must
	// have been made for.
	intent, intentText, err := resolveIntent(s.d.Store, s.ticket)
	if err != nil {
		return refuse(pickRefused("the intent could not be read: %v", err))
	}
	fp := pickFingerprint(cands, intent.Source, intentSHA256(intent.Source, intentText))
	var (
		p      pick
		reused bool
	)
	if jp := reusablePick(s.lines, s.head, fp); jp != nil {
		// The earlier answer goes through the checks a new one does, so a
		// journal line edited since cannot show what a pick could not.
		if rp, err := resolvePicks(picksResultOf(jp), cands); err == nil {
			p, reused = rp, true
		}
	}
	if !reused {
		res, err := s.dispatch(cands, intent, dir)
		if err != nil {
			return refuse(err)
		}
		if p, err = resolvePicks(res, cands); err != nil {
			return refuse(err)
		}
	}
	files, err := s.stage(dir, p)
	if err != nil {
		return refuse(err)
	}

	report.Status = PicksPicked
	if reused {
		report.Status = PicksReused
	}
	report.Flows, report.Files = len(p.flows), len(files)
	line := journal.Line{Event: "publish-picks", Commit: s.head, Outcome: report.Status, Pick: journalPick(p, fp)}
	if err := journal.Append(s.d.Store, s.ticket, line); err != nil {
		return report, nil, fmt.Errorf("verifydeliver: publish: journal publish-picks: %w", err)
	}
	rendered := renderPicksSection(p, dir, files, s.hostDirs(dir))
	return report, &rendered, nil
}

// hider returns the directory this publish stages its picked files in, and a
// function that replaces the directories jig chose with their roles in a
// reason, since a reason is journaled and printed.
func (s picksStep) hider() (string, func(string) string) {
	dir := s.picksDir()
	return dir, func(text string) string {
		return leaveOutHostPaths(text, hostDir{dir, "<picks dir>"}, hostDir{s.d.Home, "<jig home>"}, hostDir{s.d.Store.Root, "<store>"})
	}
}

// picksDir is where this publish stages its picked files: home.PicksDir for
// the head, or "" when it cannot be told (no jig home, no store id), which
// every use of it reports as a refusal.
func (s picksStep) picksDir() string {
	if s.d.Home == "" {
		return ""
	}
	id, err := s.d.Store.ID()
	if err != nil {
		return ""
	}
	dir, err := home.PicksDir(s.d.Home, id, s.ticket, s.head)
	if err != nil {
		return ""
	}
	return absPath(dir)
}

// hostDirs are the directories of this machine a pick's words must not name,
// as renderDemoSection's are: the staging directory, the jig home, the gate and
// publish leases and the store.
func (s picksStep) hostDirs(dir string) []hostDir {
	dirs := []hostDir{{dir, "<picks dir>"}, {s.d.Home, "<jig home>"}, {s.leaseDir, "<lease>"}, {s.d.Store.Root, "<store>"}}
	if gate, err := pool.Dir(s.d.Home, s.repoName, s.ticket, pool.Gate); err == nil {
		dirs = append(dirs, hostDir{gate, "<lease>"})
	}
	return dirs
}

// dispatch hands the candidates to a short pick session and returns its
// strictly parsed result. The session works in an empty scratch directory
// under the jig home, never in a lease: it reads picks.json and writes its
// result, nothing else, so there is no lease for it to leave changed. Both
// files sit beside the staging directory under the jig home, picks.json
// because it names the intent by its absolute path. It runs on the cheapest
// rung of the staircase at low effort, and asks the backend to keep no
// transcript, as the intent summarizer does.
func (s picksStep) dispatch(cands []pickCandidate, intent Intent, dir string) (PicksResult, error) {
	if s.backend == nil {
		return PicksResult{}, pickRefused("no session backend was given to pick the recordings with")
	}
	if dir == "" {
		return PicksResult{}, pickRefused("there is no jig home to keep the picked recordings in")
	}
	req := PicksRequest{Ticket: s.ticket, HeadSHA: s.head, Intent: intent}
	for _, c := range cands {
		req.Candidates = append(req.Candidates, c.PicksCandidate)
	}
	reqData, err := json.MarshalIndent(req, "", "  ")
	if err != nil {
		return PicksResult{}, pickRefused("picks.json could not be built: %v", err)
	}

	reqPath, resultPath := dir+".picks.json", dir+".result.json"
	if err := media.PlainParents(absPath(filepath.Join(s.d.Home, "evidence")), dir); err != nil {
		return PicksResult{}, err
	}
	if err := os.MkdirAll(filepath.Dir(dir), 0o755); err != nil {
		return PicksResult{}, pickRefused("the picks directory could not be made: %v", err)
	}
	if err := os.WriteFile(reqPath, reqData, 0o644); err != nil {
		return PicksResult{}, pickRefused("picks.json could not be written: %v", err)
	}
	// A result left by an earlier publish must never be read as this one's.
	if err := os.Remove(resultPath); err != nil && !os.IsNotExist(err) {
		return PicksResult{}, pickRefused("an earlier pick result could not be cleared: %v", err)
	}
	scratch, err := makeIntentScratchDir(s.d.Home)
	if err != nil {
		return PicksResult{}, pickRefused("the pick session's scratch directory could not be made: %v", err)
	}
	defer func() { _ = os.RemoveAll(scratch) }()

	err = s.backend.Run(session.Dispatch{
		Ticket:     s.ticket,
		Slice:      session.PublishPicksSlice,
		Attempt:    1,
		Worktree:   scratch,
		SliceJSON:  reqPath,
		ResultJSON: resultPath,
		Model:      staircase.Select(s.d.Rungs, staircase.Signals{}),
		Effort:     picksEffort,
		Prompt:     RenderPicksPrompt(reqPath, resultPath),
		Screen:     true,
		// The session's input is a list of captions; its transcript would be a
		// directory named after a scratch directory that nothing removes.
		NoSessionPersistence: true,
	})
	if err != nil {
		return PicksResult{}, pickFailed(err)
	}
	data, err := os.ReadFile(resultPath)
	if err != nil {
		return PicksResult{}, pickFailed(&axi.Error{Msg: fmt.Sprintf("it wrote no pick result (%v)", err), Code: "PICK_NO_RESULT"})
	}
	// A backend writes a result of its own, a slice result's shape, when the
	// session wrote none: not a pick result that failed the contract.
	if outcome, summary, ok := backendFallback(data); ok {
		return PicksResult{}, pickFailed(&axi.Error{
			Msg:  fmt.Sprintf("it wrote no pick result, and the backend recorded its own outcome in its place (%s: %s)", outcome, summary),
			Code: "PICK_NO_RESULT",
		})
	}
	return ParsePicksResult(data)
}

// stage copies the files p shows into dir, a fresh directory of jig's own,
// named rec-<n>.<ext> in the order the pull request shows them, and returns
// them as the host attaches them: each file is checked as it is copied (a
// regular file of the recorded size whose bytes hash to the recorded sha256,
// read through the handle that was checked), because the session has run
// since the candidates were checked. The directory is pinned once it is made,
// and checked to be that very directory before each file is created in it.
// Whatever dir held before is gone, so a pick of fewer files than an earlier
// pick of the same head leaves none of the earlier one's behind. A file that
// fails refuses the whole pick and leaves no directory.
func (s picksStep) stage(dir string, p pick) (files []DemoFile, err error) {
	if dir == "" {
		return nil, pickRefused("there is no jig home to keep the picked recordings in")
	}
	storeID, err := s.d.Store.ID()
	if err != nil {
		return nil, pickRefused("the store id could not be resolved: %v", err)
	}
	top := absPath(filepath.Join(s.d.Home, "evidence"))
	if err := media.PlainParents(top, dir); err != nil {
		return nil, err
	}
	// RemoveAll does not follow a link: one in the way is removed as itself.
	if err := os.RemoveAll(dir); err != nil {
		return nil, pickRefused("the picks directory could not be cleared: %v", err)
	}
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return nil, pickRefused("the picks directory could not be made: %v", err)
	}
	var made os.FileInfo
	defer func() {
		if err != nil {
			removeStaging(top, dir, made)
		}
	}()
	made, err = media.LstatPinned(dir)
	if err != nil || made.Mode().Type() != os.ModeDir {
		return nil, pickRefused("the picks directory is not a plain directory")
	}
	items := p.items()
	files = make([]DemoFile, 0, len(items))
	for i, it := range items {
		src, err := it.cand.path(s.d, storeID, s.ticket)
		if err != nil {
			return nil, pickRefused("the recording %s cannot be found: %v", it.cand.rec.File, err)
		}
		name := fmt.Sprintf("rec-%d.%s", i+1, it.cand.ext())
		if err := copyRecording(top, src, dir, made, name, it.cand.rec); err != nil {
			return nil, err
		}
		files = append(files, DemoFile{Name: name, SHA256: it.cand.rec.SHA256, Size: it.cand.rec.Size, Caption: it.shownCaption()})
	}
	return files, nil
}

// copyRecording copies the recording rec from src to dir/name, which must not
// exist, and refuses when src is not what rec describes or dir is no longer
// the directory made, which stage pinned when it made it. src is checked
// and opened and the very handle is read and hashed as it is copied, so a file
// swapped or grown after the check is refused, not copied.
func copyRecording(top, src, dir string, made os.FileInfo, name string, rec journal.Recording) error {
	if err := media.PlainParents(top, src); err != nil {
		return pickRefused("a directory above the recording %s is a link", rec.File)
	}
	info, err := media.LstatPinned(src)
	switch {
	case err != nil:
		return pickRefused("the recording %s cannot be read", rec.File)
	case !info.Mode().IsRegular():
		return pickRefused("the recording %s is not a regular file", rec.File)
	case info.Size() != rec.Size:
		return pickRefused("the recording %s changed size", rec.File)
	}
	in, err := os.Open(src)
	if err != nil {
		return pickRefused("the recording %s cannot be read", rec.File)
	}
	defer in.Close()
	if opened, err := in.Stat(); err != nil || !os.SameFile(info, opened) {
		return pickRefused("the recording %s changed while it was being read", rec.File)
	}
	if cur, err := media.LstatPinned(dir); err != nil || cur.Mode().Type() != os.ModeDir || !os.SameFile(made, cur) {
		return pickRefused("the picks directory was replaced while the recordings were being staged")
	}
	dst := filepath.Join(dir, name)
	out, err := os.OpenFile(dst, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o644)
	if err != nil {
		return pickRefused("the recording %s could not be staged: %v", rec.File, err)
	}
	h := sha256.New()
	n, err := io.Copy(io.MultiWriter(out, h), io.LimitReader(in, info.Size()+1))
	if cerr := out.Close(); err == nil {
		err = cerr
	}
	switch {
	case err != nil:
		return pickRefused("the recording %s could not be staged: %v", rec.File, err)
	case n != info.Size() || hex.EncodeToString(h.Sum(nil)) != rec.SHA256:
		_ = os.Remove(dst)
		return pickRefused("the recording %s changed while it was being staged", rec.File)
	}
	return nil
}

// verifyStaged checks, after the confirmation and before anything is pushed,
// that dir is still a plain directory under plain parents and that every staged
// file in it is still the regular file of the recorded size and sha256 stage
// made. The files are attached by name from dir, so a file swapped or edited
// between staging and the attach call would otherwise be the one uploaded. It
// runs before the push so that a refusal leaves origin as it was.
func verifyStaged(top, dir string, files []DemoFile) error {
	if err := media.PlainParents(top, dir); err != nil {
		return err
	}
	info, err := media.LstatPinned(dir)
	if err != nil || info.Mode().Type() != os.ModeDir {
		return fmt.Errorf("the staging directory is not a plain directory")
	}
	for _, f := range files {
		path := filepath.Join(dir, f.Name)
		fi, err := media.LstatPinned(path)
		switch {
		case err != nil:
			return fmt.Errorf("%s cannot be read", f.Name)
		case !fi.Mode().IsRegular():
			return fmt.Errorf("%s is not a regular file", f.Name)
		case fi.Size() != f.Size:
			return fmt.Errorf("%s changed size", f.Name)
		}
		if sum, err := media.HashRegularFile(path, fi); err != nil || sum != f.SHA256 {
			return fmt.Errorf("%s changed", f.Name)
		}
	}
	return nil
}

// removeStaging removes dir, the staging directory of a staging that failed,
// unless it cannot be sure what it would remove: RemoveAll goes by path, and
// one that followed a link swapped in for a directory above dir, or for dir
// itself, would remove what the link leads to. So a directory above dir that is
// no longer plain, or a dir that is no longer the directory stage made (made,
// pinned when it was made; nil when it never was), is left for the operator.
// A link is never followed: RemoveAll removes one as itself.
func removeStaging(top, dir string, made os.FileInfo) {
	if media.PlainParents(top, dir) != nil {
		return
	}
	cur, err := media.LstatPinned(dir)
	if err != nil {
		return
	}
	// A link is never the pinned staging directory, whatever its identity
	// says: on Linux a link made where the directory was removed can be
	// handed the freed inode number, and os.SameFile compares only device and
	// inode. (Unpinned, a link is removed as itself: RemoveAll never follows
	// it.)
	if made != nil && (cur.Mode().Type() != os.ModeDir || !os.SameFile(made, cur)) {
		return
	}
	_ = os.RemoveAll(dir)
}
