package main

import (
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"

	"github.com/develdeco/jig/internal/fixture"
	"github.com/develdeco/jig/internal/store"
)

var hashMarker = regexp.MustCompile(`@HASH:([^"]+)`)

// newStoreOnlyFixture is the store of the fixture ticket made of files alone:
// the ticket JIG-1 with the fixture's brief and four slices, in a project of no
// repos. It carries the fields of a fixture.Fixture that a test of what the
// store says reads (Ticket, StoreDir, Home), and starts no git and copies no
// repository, where fixture.Generate copies about 150 files and makes about 110
// directories (it starts no git either, once its template exists): a test that
// renders `jig status` or walks the journal of a ticket needs the store and
// nothing around it. A test that runs a verb that commits the store, or reads
// a clone, takes newFixture.
func newStoreOnlyFixture(t *testing.T) *fixture.Fixture {
	t.Helper()
	root := t.TempDir()
	put := func(path string, content []byte) {
		t.Helper()
		full := filepath.Join(root, filepath.FromSlash(path))
		if err := os.MkdirAll(filepath.Dir(full), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(full, content, 0o644); err != nil {
			t.Fatal(err)
		}
	}
	read := func(name string) []byte {
		t.Helper()
		data, err := os.ReadFile(filepath.Join("..", "..", "testdata", "fixture", name))
		if err != nil {
			t.Fatal(err)
		}
		return []byte(strings.ReplaceAll(string(data), "\r\n", "\n"))
	}
	put("project.yaml", []byte("schema_version: 2\nname: fixture\nkeys:\n  JIG: everything in fixture\ntrackers: []\nplatform: platform/\n"))
	put("ledger.md", []byte("# Ledger\n"))
	brief := read("brief.md")
	hashes := store.BriefSectionHashes(brief)
	slices := hashMarker.ReplaceAllStringFunc(string(read("slices.yaml")), func(m string) string {
		heading := strings.TrimSpace(hashMarker.FindStringSubmatch(m)[1])
		h, ok := hashes[heading]
		if !ok {
			t.Fatalf("the fixture brief has no section %q", heading)
		}
		return h
	})
	put("tickets/"+fixture.Ticket+"/brief.md", brief)
	put("tickets/"+fixture.Ticket+"/slices.yaml", []byte(slices))
	return &fixture.Fixture{Ticket: fixture.Ticket, StoreDir: root, Home: t.TempDir()}
}
