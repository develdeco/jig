package recall

import (
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
)

func writeTree(t *testing.T, root string, files map[string]string) {
	t.Helper()
	for name, content := range files {
		path := filepath.Join(root, filepath.FromSlash(name))
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
			t.Fatal(err)
		}
	}
}

func TestReadDocsListsMarkdownBelowThePlatformDirectory(t *testing.T) {
	t.Parallel()
	root := t.TempDir()
	writeTree(t, root, map[string]string{
		"platform/contract-index.md": "- JIG-1: workspaces alpha\n",
		"platform/api/errors.MD":     "# Errors\n",
		"platform/notes.txt":         "not markdown",
		"platform/.hidden/secret.md": "# Hidden\n",
		"other/readme.md":            "# Not platform\n",
	})
	docs, err := readDocs(root, "platform/")
	if err != nil {
		t.Fatal(err)
	}
	var paths []string
	for _, d := range docs {
		paths = append(paths, d.Path)
	}
	if want := []string{"platform/api/errors.MD", "platform/contract-index.md"}; !reflect.DeepEqual(paths, want) {
		t.Errorf("docs = %q, want %q", paths, want)
	}
	if docs[1].Bytes != len("- JIG-1: workspaces alpha\n") || docs[1].Text != "- JIG-1: workspaces alpha\n" {
		t.Errorf("doc = %+v, want its size and text", docs[1])
	}
}

func TestReadDocsOfAnAbsentOrConfiguredDirectory(t *testing.T) {
	t.Parallel()
	root := t.TempDir()
	if docs, err := readDocs(root, ""); err != nil || len(docs) != 0 {
		t.Errorf("readDocs of a store with no platform directory = %v, %v, want none", docs, err)
	}
	writeTree(t, root, map[string]string{"contracts/a.md": "# A\n"})
	docs, err := readDocs(root, "./contracts")
	if err != nil || len(docs) != 1 || docs[0].Path != "contracts/a.md" {
		t.Errorf("readDocs of a configured directory = %+v, %v, want contracts/a.md", docs, err)
	}
	for _, outside := range []string{"..", "../elsewhere", "/etc"} {
		if _, err := readDocs(root, outside); err == nil || !strings.Contains(err.Error(), "not inside the store") {
			t.Errorf("readDocs(%q) = %v, want a refusal: the directory leaves the store", outside, err)
		}
	}
}

func TestDocOfTakesTitleHeadAndMatches(t *testing.T) {
	t.Parallel()
	text := "\n# Error codes\n\nEvery refusal carries a code.\nline three\nline four\nline five\nline six\nA retry follows a NETWORK code.\nunrelated\nretry again\n"
	d := docOf(DocText{Path: "platform/errors.md", Bytes: len(text), Text: text}, words("retry network"))
	if d.Title != "Error codes" {
		t.Errorf("title = %q, want the first heading", d.Title)
	}
	wantHead := []string{"# Error codes", "Every refusal carries a code.", "line three", "line four", "line five"}
	if !reflect.DeepEqual(d.Head, wantHead) {
		t.Errorf("head = %q, want %q", d.Head, wantHead)
	}
	wantMatches := []string{"A retry follows a NETWORK code.", "retry again"}
	if !reflect.DeepEqual(d.Matches, wantMatches) {
		t.Errorf("matches = %q, want the later lines that share a word with the query %q", d.Matches, wantMatches)
	}

	plain := docOf(DocText{Path: "platform/x.md", Text: "first line\nsecond line\n"}, nil)
	if plain.Title != "first line" || len(plain.Matches) != 0 {
		t.Errorf("a document with no heading = %+v, want its first line as the title and no matches", plain)
	}
}
