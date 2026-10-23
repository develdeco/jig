package project

import (
	"os"
	"path/filepath"
	"reflect"
	"sync"
	"testing"

	"github.com/develdeco/jig/internal/home"
)

func TestIgnoreAndUnignore(t *testing.T) {
	t.Parallel()
	jigHome := t.TempDir()
	a := filepath.Join(t.TempDir(), "a")
	b := filepath.Join(t.TempDir(), "b")

	if got, err := LoadIgnored(jigHome); err != nil || len(got) != 0 {
		t.Fatalf("LoadIgnored with no list = %v, %v, want none", got, err)
	}
	if _, err := os.Stat(home.IgnoredPath(jigHome)); err == nil {
		t.Fatal("reading wrote the list")
	}

	if added, err := Ignore(jigHome, a); err != nil || !added {
		t.Fatalf("Ignore(a) = %v, %v, want added", added, err)
	}
	if added, err := Ignore(jigHome, a); err != nil || added {
		t.Fatalf("Ignore(a) again = %v, %v, want already there", added, err)
	}
	// A directory under an ignored one is covered, so adds nothing.
	if added, err := Ignore(jigHome, filepath.Join(a, "inner")); err != nil || added {
		t.Fatalf("Ignore(a/inner) = %v, %v, want covered by a", added, err)
	}
	if added, err := Ignore(jigHome, b); err != nil || !added {
		t.Fatalf("Ignore(b) = %v, %v, want added", added, err)
	}
	got, err := LoadIgnored(jigHome)
	if err != nil || !reflect.DeepEqual(got, []string{a, b}) {
		t.Fatalf("LoadIgnored = %v, %v, want a then b", got, err)
	}

	if dir, ok := IsIgnored(got, filepath.Join(a, "x", "y")); !ok || dir != a {
		t.Errorf("IsIgnored(a/x/y) = %q, %v, want a", dir, ok)
	}
	if _, ok := IsIgnored(got, a+"-sibling"); ok {
		t.Error("a sibling with a's name as a prefix counted as ignored")
	}
	if _, ok := IsIgnored(got, filepath.Dir(a)); ok {
		t.Error("the parent of an ignored directory counted as ignored")
	}

	// Removing only takes out the directory's own entry.
	if removed, err := Unignore(jigHome, filepath.Join(a, "inner")); err != nil || removed {
		t.Fatalf("Unignore(a/inner) = %v, %v, want nothing removed", removed, err)
	}
	if removed, err := Unignore(jigHome, a); err != nil || !removed {
		t.Fatalf("Unignore(a) = %v, %v, want removed", removed, err)
	}
	if got, _ := LoadIgnored(jigHome); !reflect.DeepEqual(got, []string{b}) {
		t.Fatalf("after Unignore(a): %v, want only b", got)
	}
	if removed, err := Unignore(jigHome, a); err != nil || removed {
		t.Fatalf("Unignore(a) again = %v, %v, want nothing removed", removed, err)
	}

	if _, err := LoadIgnored(""); err == nil {
		t.Error("LoadIgnored with no jig home: no error")
	}
	if _, err := Ignore("", a); err == nil {
		t.Error("Ignore with no jig home: no error")
	}
}

// TestIgnoreConcurrent: ignoring from several goroutines loses no entry.
func TestIgnoreConcurrent(t *testing.T) {
	t.Parallel()
	jigHome := t.TempDir()
	base := t.TempDir()
	const n = 12
	var wg sync.WaitGroup
	for i := 0; i < n; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			if _, err := Ignore(jigHome, filepath.Join(base, "dir"+string(rune('a'+i)))); err != nil {
				t.Error(err)
			}
		}()
	}
	wg.Wait()
	if got, err := LoadIgnored(jigHome); err != nil || len(got) != n {
		t.Fatalf("LoadIgnored = %d entries, %v, want %d", len(got), err, n)
	}
}
