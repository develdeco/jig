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
// and with "D" returned, both ways round (a path added one way is deleted the
// other).
func TestDiffNameStatusGivesWhatTheTwoNameOnlyCallsGave(t *testing.T) {
	dir := streamRepo(t, "diffstatus", diffStatusStream)
	for _, c := range []struct {
		name, base, head string
		changed, deleted int
	}{
		{"the whole change", "main~1", "main", 5, 3}, // a rename is both
		{"the other way", "main", "main~1", 5, 3},
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
		if len(changed) != c.changed || len(deleted) != c.deleted {
			t.Errorf("%s: %d changed and %d deleted, want %d and %d", c.name, len(changed), len(deleted), c.changed, c.deleted)
		}
	}
	if changed, deleted, err := DiffNameStatus(dir, "main", "main"); err != nil || changed != nil || deleted != nil {
		t.Errorf("no change = %q and %q, %v; want nothing", changed, deleted, err)
	}
}
