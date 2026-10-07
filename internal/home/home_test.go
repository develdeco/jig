package home

import (
	"path/filepath"
	"testing"
)

func TestRootHonorsEnv(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("JIG_HOME", dir)
	r, err := Root()
	if err != nil {
		t.Fatal(err)
	}
	if r != dir {
		t.Fatalf("Root() = %q, want %q", r, dir)
	}
}

// TestPathsDeriveFromTheRootGiven covers the home-anchored paths: they are
// derived from the root a caller passes, never from the environment.
func TestPathsDeriveFromTheRootGiven(t *testing.T) {
	root := t.TempDir()
	t.Setenv("JIG_HOME", t.TempDir())
	if p := PoolDir(root); p != filepath.Join(root, "pool") {
		t.Fatalf("PoolDir(%q) = %q", root, p)
	}
	if m := MachinePath(root); m != filepath.Join(root, "projects.yaml") {
		t.Fatalf("MachinePath(%q) = %q", root, m)
	}
	if e := IntentExcerptDir(root); e != filepath.Join(root, "intent-excerpts") {
		t.Fatalf("IntentExcerptDir(%q) = %q", root, e)
	}
	if s := IntentScratchDir(root); s != filepath.Join(root, "intent-scratch") {
		t.Fatalf("IntentScratchDir(%q) = %q", root, s)
	}
}

func TestEvidenceDirIsUnderTheRootGiven(t *testing.T) {
	root := t.TempDir()
	t.Setenv("JIG_HOME", t.TempDir())
	got, err := EvidenceDir(root, "0123456789abcdef", "T-1", "abc123")
	if err != nil {
		t.Fatal(err)
	}
	if want := filepath.Join(root, "evidence", "0123456789abcdef", "T-1", "abc123"); got != want {
		t.Fatalf("EvidenceDir = %q, want %q", got, want)
	}
}

// TestEvidenceDirRefusesAnythingButASingleDirectoryName checks each of the
// three caller-supplied parts: a spelling that climbs out of the evidence
// tree, or names two directories, or a directory Windows would fold onto
// another, is refused rather than joined.
func TestEvidenceDirRefusesAnythingButASingleDirectoryName(t *testing.T) {
	bad := []string{"", ".", "..", ".hidden", "a/b", `a\b`, "C:x", "x.", "x "}
	for _, name := range bad {
		for i, parts := range [][3]string{{name, "T-1", "abc"}, {"id", name, "abc"}, {"id", "T-1", name}} {
			if got, err := EvidenceDir(t.TempDir(), parts[0], parts[1], parts[2]); err == nil {
				t.Errorf("EvidenceDir with %q in part %d = %q, want a refusal", name, i, got)
			}
		}
	}
}

// TestRecordDirIsOneDirectoryPerRunUnderTheTicketsRecordings: a builder's
// recordings live in a recordings directory of the ticket's evidence, one
// directory per commit and, below it, one per oracle run, so they never share
// a name with a gate head's demo media and a later run never lands in an
// earlier run's directory. The path is derived from the root given alone.
func TestRecordDirIsOneDirectoryPerRunUnderTheTicketsRecordings(t *testing.T) {
	t.Parallel()
	root := t.TempDir()
	got, err := RecordDir(root, "0123456789abcdef", "T-1", "abc123", "a-a1-f0")
	if err != nil {
		t.Fatal(err)
	}
	if want := filepath.Join(root, "evidence", "0123456789abcdef", "T-1", "recordings", "abc123", "a-a1-f0"); got != want {
		t.Fatalf("RecordDir = %q, want %q", got, want)
	}
	other, err := RecordDir(root, "0123456789abcdef", "T-1", "abc123", "a-a2-f0")
	if err != nil || other == got {
		t.Fatalf("a second run's directory = %q, %v; want one of its own", other, err)
	}
}

// TestRecordDirRefusesAnythingButASingleDirectoryName holds each of
// RecordDir's four parts to the rule EvidenceDir's are held to.
func TestRecordDirRefusesAnythingButASingleDirectoryName(t *testing.T) {
	t.Parallel()
	bad := []string{"", ".", "..", ".hidden", "a/b", `a\b`, "C:x", "x.", "x "}
	for _, name := range bad {
		for i, parts := range [][4]string{{name, "T-1", "abc", "r"}, {"id", name, "abc", "r"}, {"id", "T-1", name, "r"}, {"id", "T-1", "abc", name}} {
			if got, err := RecordDir(t.TempDir(), parts[0], parts[1], parts[2], parts[3]); err == nil {
				t.Errorf("RecordDir with %q in part %d = %q, want a refusal", name, i, got)
			}
		}
	}
}

// TestPicksDirIsOneDirectoryPerHeadUnderTheTicketsPicks: the files a publish
// picked are staged in a picks directory of the ticket's evidence, one
// directory per head, never sharing a name with a gate head's demo media or
// the recordings.
func TestPicksDirIsOneDirectoryPerHeadUnderTheTicketsPicks(t *testing.T) {
	t.Parallel()
	root := t.TempDir()
	got, err := PicksDir(root, "0123456789abcdef", "T-1", "abc123")
	if err != nil {
		t.Fatal(err)
	}
	if want := filepath.Join(root, "evidence", "0123456789abcdef", "T-1", "picks", "abc123"); got != want {
		t.Fatalf("PicksDir = %q, want %q", got, want)
	}
	media, err := EvidenceDir(root, "0123456789abcdef", "T-1", "abc123")
	if err != nil || media == got {
		t.Fatalf("a head's demo media directory = %q, %v; want one other than the picks directory", media, err)
	}
}

// TestPicksDirRefusesAnythingButASingleDirectoryName holds each of PicksDir's
// three parts to the rule EvidenceDir's are held to.
func TestPicksDirRefusesAnythingButASingleDirectoryName(t *testing.T) {
	t.Parallel()
	bad := []string{"", ".", "..", ".hidden", "a/b", `a\b`, "C:x", "x.", "x "}
	for _, name := range bad {
		for i, parts := range [][3]string{{name, "T-1", "abc"}, {"id", name, "abc"}, {"id", "T-1", name}} {
			if got, err := PicksDir(t.TempDir(), parts[0], parts[1], parts[2]); err == nil {
				t.Errorf("PicksDir with %q in part %d = %q, want a refusal", name, i, got)
			}
		}
	}
}

// TestReviewDirIsOneDirectoryPerRoundUnderTheTicketsReviews: what a round
// hands its reviewer from the jig home lives in a directory of the round's own,
// under a "reviews" directory of the ticket's evidence, never sharing a name
// with a gate head's demo media, the recordings or the picks.
func TestReviewDirIsOneDirectoryPerRoundUnderTheTicketsReviews(t *testing.T) {
	t.Parallel()
	root := t.TempDir()
	got, err := ReviewDir(root, "0123456789abcdef", "T-1", 2)
	if err != nil {
		t.Fatal(err)
	}
	if want := filepath.Join(root, "evidence", "0123456789abcdef", "T-1", "reviews", "round-2"); got != want {
		t.Fatalf("ReviewDir = %q, want %q", got, want)
	}
	other, err := ReviewDir(root, "0123456789abcdef", "T-1", 3)
	if err != nil || other == got {
		t.Fatalf("round 3's directory = %q, %v; want one other than round 2's", other, err)
	}
}

// TestReviewDirRefusesAnythingButASingleDirectoryName holds ReviewDir's store
// id and ticket to the rule EvidenceDir's are held to.
func TestReviewDirRefusesAnythingButASingleDirectoryName(t *testing.T) {
	t.Parallel()
	bad := []string{"", ".", "..", ".hidden", "a/b", `a\b`, "C:x", "x.", "x "}
	for _, name := range bad {
		for i, parts := range [][2]string{{name, "T-1"}, {"id", name}} {
			if got, err := ReviewDir(t.TempDir(), parts[0], parts[1], 1); err == nil {
				t.Errorf("ReviewDir with %q in part %d = %q, want a refusal", name, i, got)
			}
		}
	}
}
