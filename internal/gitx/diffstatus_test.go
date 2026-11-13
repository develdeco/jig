package gitx

import (
	"reflect"
	"testing"
)

// diffStatusStream: a base, and a head that adds, modifies, deletes and
// changes the type of files, among them paths with a space.
const diffStatusStream = `commit refs/heads/main
committer t <t@example.invalid> 1700000000 +0000
data 4
base
M 100644 inline keep.txt
data 4
keep
M 100644 inline mod.txt
data 3
one
M 100644 inline del.txt
data 3
del
M 100644 inline "sp ace.txt"
data 3
del
M 100644 inline tc.txt
data 4
file
M 100644 inline old-name.txt
data 7
renamed

commit refs/heads/main
committer t <t@example.invalid> 1700000100 +0000
data 4
head
M 100644 inline mod.txt
data 3
two
D del.txt
D "sp ace.txt"
M 100644 inline "new sp ace.txt"
data 3
new
M 100644 inline added.txt
data 3
new
M 120000 inline tc.txt
data 8
keep.txt
D old-name.txt
M 100644 inline new-name.txt
data 7
renamed

`

// TestDiffNameStatusGivesWhatTheTwoNameOnlyCallsGave: the changed and the
// deleted paths of one diff, in git's order, are what DiffNameOnly with "AMT"
// and with "D" returned.
func TestDiffNameStatusGivesWhatTheTwoNameOnlyCallsGave(t *testing.T) {
	dir := t.TempDir()
	if _, err := Run(dir, "init", "-q", "-b", "main"); err != nil {
		t.Fatal(err)
	}
	if _, err := RunInput(dir, []byte(diffStatusStream), "fast-import", "--quiet"); err != nil {
		t.Fatalf("fast-import: %v", err)
	}
	for _, c := range []struct{ name, base, head string }{
		{"the whole change", "main~1", "main"},
		{"no change", "main", "main"},
		{"the other way", "main", "main~1"},
	} {
		wantChanged, err := DiffNameOnly(dir, c.base, c.head, "AMT")
		if err != nil {
			t.Fatal(err)
		}
		wantDeleted, err := DiffNameOnly(dir, c.base, c.head, "D")
		if err != nil {
			t.Fatal(err)
		}
		changed, deleted, err := DiffNameStatus(dir, c.base, c.head)
		if err != nil {
			t.Fatalf("%s: DiffNameStatus: %v", c.name, err)
		}
		if !reflect.DeepEqual(changed, wantChanged) || !reflect.DeepEqual(deleted, wantDeleted) {
			t.Errorf("%s: changed %q deleted %q, want %q and %q", c.name, changed, deleted, wantChanged, wantDeleted)
		}
	}
	changed, deleted, err := DiffNameStatus(dir, "main~1", "main")
	if err != nil || len(changed) != 5 || len(deleted) != 3 {
		t.Errorf("the whole change = %q and %q, %v; want five changed and three deleted (a rename is both)", changed, deleted, err)
	}
}
