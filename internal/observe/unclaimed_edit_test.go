package observe

import (
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"github.com/develdeco/jig/internal/phase"
)

// buildPhase is a ticket at its build phase whose slices are the sub-steps
// given (slice id -> sub state), each in the state a claim leaves it in.
func buildPhase(slices map[string]string, order ...string) phase.Ticket {
	var subs []phase.Sub
	for _, id := range order {
		subs = append(subs, phase.Sub{Key: "slice:" + id, State: slices[id]})
	}
	t := phase.Ticket{Current: phase.Build, Ready: true}
	t.Phases[1] = phase.Phase{Name: phase.Build, State: "current", Subs: subs}
	return t
}

// briefPhase is a ticket whose brief waits for its approval.
func briefPhase() phase.Ticket {
	t := phase.Ticket{Current: phase.Brief, Ready: true}
	t.Phases[0] = phase.Phase{Name: phase.Brief, State: "current", Subs: []phase.Sub{
		{Key: "drafted", State: phase.SubDone},
		{Key: "slices", State: phase.SubDone},
		{Key: "approved", State: phase.SubNeedsYou},
	}}
	return t
}

// edit is the context the session hears for an Edit of the files, in the
// clone unless a file is absolute elsewhere.
func (r *phaseRig) edit(files ...string) string {
	return r.handle(r.ev(KindTool, func(e *Event) { e.Tool, e.Files = "Edit", files })).Context
}

func (r *phaseRig) inClone(name string) string { return filepath.Join(r.fx.RepoDir, name) }

func TestAnEditWithNoSliceClaimedHearsToClaimOne(t *testing.T) {
	t.Parallel()
	r := newPhaseRig(t)
	r.onTicketBranch()
	r.phases[r.fx.Ticket] = buildPhase(map[string]string{"a": phase.SubPending, "b": phase.SubWaiting, "c": phase.SubDone}, "a", "b", "c")
	r.handle(r.ev(KindSessionStart))
	got := r.edit(r.inClone("main.go"))
	for _, want := range []string{"jig claim " + r.fx.Ticket + " --slice", "ready: a"} {
		if !strings.Contains(got, want) {
			t.Errorf("the note lacks %q:\n%s", want, got)
		}
	}
	if strings.Contains(got, "ready: a,") {
		t.Errorf("the note names a slice that is not ready:\n%s", got)
	}
	if again := r.edit(r.inClone("other.go")); strings.Contains(again, "jig claim") {
		t.Errorf("the note was told twice:\n%s", again)
	}
	lines := r.ledgerOf("note-delivered")
	if len(lines) != 1 || lines[0].Ref != "unclaimed-edit:"+r.fx.Ticket+":claim" {
		t.Errorf("note-delivered lines = %+v, want one with the ref unclaimed-edit:%s:claim", lines, r.fx.Ticket)
	}
}

func TestTheCheckoutMatchFoldsCaseOnWindows(t *testing.T) {
	t.Parallel()
	if runtime.GOOS != "windows" {
		t.Skip("paths are case-sensitive here")
	}
	r := newPhaseRig(t)
	r.onTicketBranch()
	r.phases[r.fx.Ticket] = buildPhase(map[string]string{"a": phase.SubPending}, "a")
	r.handle(r.ev(KindSessionStart))
	file := r.inClone("main.go")
	flipped := strings.ToUpper(file[:1]) + file[1:]
	if file[0] >= 'A' && file[0] <= 'Z' {
		flipped = strings.ToLower(file[:1]) + file[1:]
	}
	if got := r.edit(flipped); !strings.Contains(got, "jig claim") {
		t.Errorf("an edit of %s: context = %s, want the claim note", flipped, got)
	}
}

func TestAnEditThroughASymlinkedCheckoutIsInside(t *testing.T) {
	t.Parallel()
	for _, viaLink := range []bool{false, true} {
		r := newPhaseRig(t)
		link := filepath.Join(t.TempDir(), "link")
		if err := os.Symlink(r.fx.RepoDir, link); err != nil {
			t.Skipf("symlinks are not permitted here: %v", err)
		}
		r.onTicketBranch()
		r.phases[r.fx.Ticket] = buildPhase(map[string]string{"a": phase.SubPending}, "a")
		r.handle(r.ev(KindSessionStart))
		// The checkout and the edited file are named by different routes, the
		// file one not yet created.
		cwd, file := link, filepath.Join(r.fx.RepoDir, "new", "file.go")
		if viaLink {
			cwd, file = r.fx.RepoDir, filepath.Join(link, "new", "file.go")
		}
		got := r.handle(r.ev(KindTool, func(e *Event) {
			e.Cwd, e.Tool, e.Files = cwd, "Edit", []string{file}
		})).Context
		if !strings.Contains(got, "jig claim") {
			t.Errorf("cwd %s, file %s heard no claim note:\n%s", cwd, file, got)
		}
	}
}

// TestAnEditThroughAJunctionedCheckoutIsInside: a Windows junction is a link
// the checkout match follows as it follows a symlink, whichever of the cwd
// and the edited file is named through it, for a file at the checkout's top
// folder and one below it, and from a cwd in a subdirectory of the checkout.
func TestAnEditThroughAJunctionedCheckoutIsInside(t *testing.T) {
	t.Parallel()
	if runtime.GOOS != "windows" {
		t.Skip("directory junctions only exist on Windows")
	}
	for _, c := range []struct {
		name string
		// cwdVia and fileVia name the cwd and the file through the junction
		// rather than the checkout; cwdSub is a subdirectory of the base the
		// cwd is in, file is relative to its base.
		cwdVia, fileVia bool
		cwdSub, file    string
	}{
		{"cwd through the junction, file below the top folder", true, false, "", "new/file.go"},
		{"file through the junction, below the top folder", false, true, "", "new/file.go"},
		{"file through the junction, at the top folder", false, true, "", "go.mod"},
		{"file through the junction, cwd in a subdirectory", false, true, "pkg", "new/file.go"},
	} {
		r := newPhaseRig(t)
		link := filepath.Join(t.TempDir(), "junction")
		out, err := exec.Command("cmd", "/c", "mklink", "/J", link, r.fx.RepoDir).CombinedOutput()
		if err != nil {
			t.Skipf("a junction cannot be made here: %v\n%s", err, out)
		}
		// Removed before the temp dir holding it, so the cleanup never walks
		// through the junction into the checkout.
		t.Cleanup(func() { os.Remove(link) })
		r.onTicketBranch()
		r.phases[r.fx.Ticket] = buildPhase(map[string]string{"a": phase.SubPending}, "a")
		r.handle(r.ev(KindSessionStart))
		cwdBase, fileBase := r.fx.RepoDir, r.fx.RepoDir
		if c.cwdVia {
			cwdBase = link
		}
		if c.fileVia {
			fileBase = link
		}
		cwd := filepath.Join(cwdBase, c.cwdSub)
		if c.cwdSub != "" {
			if err := os.MkdirAll(cwd, 0o755); err != nil {
				t.Fatal(err)
			}
		}
		file := filepath.Join(fileBase, filepath.FromSlash(c.file))
		got := r.handle(r.ev(KindTool, func(e *Event) {
			e.Cwd, e.Tool, e.Files = cwd, "Edit", []string{file}
		})).Context
		if !strings.Contains(got, "jig claim") {
			t.Errorf("cwd %s, file %s heard no claim note:\n%s", cwd, file, got)
		}
	}
}
