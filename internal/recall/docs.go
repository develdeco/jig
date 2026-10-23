package recall

import (
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
)

// Doc is one markdown document of the store's platform directory: where it is,
// what it is called, how big it is, its first lines and, when the report was
// asked about something, the later lines that share a word with the query.
type Doc struct {
	Path    string   `json:"path"`
	Title   string   `json:"title,omitempty"`
	Bytes   int      `json:"bytes"`
	Head    []string `json:"head"`
	Matches []string `json:"matches,omitempty"`
}

// The caps on what platform documents contribute.
const (
	// MaxDocs is the most documents a report lists.
	MaxDocs = 40
	// MaxDocLines is the most head lines, and the most matching lines, of one
	// document.
	MaxDocLines = 5
	// maxDocBytes is the most of one document that is read.
	maxDocBytes = 256 << 10
)

// DocText is a platform document as Read took it from disk.
type DocText struct {
	Path  string // slash-separated, relative to the store root
	Bytes int    // the file's size
	Text  string // at most maxDocBytes of it
}

// readDocs reads the markdown files below the store's platform directory,
// skipping dot directories, in path order. dir is the directory as the
// project config names it ("platform/"); one that is empty means "platform/",
// and one that leaves the store is refused. An absent directory has no
// documents.
func readDocs(root, dir string) ([]DocText, error) {
	if strings.TrimSpace(dir) == "" {
		dir = "platform/"
	}
	rel := filepath.FromSlash(dir)
	if !filepath.IsLocal(rel) {
		return nil, fmt.Errorf("recall: the platform directory %q is not inside the store", dir)
	}
	base := filepath.Join(root, rel)
	var docs []DocText
	err := filepath.WalkDir(base, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			if os.IsNotExist(err) && path == base {
				return fs.SkipAll
			}
			return err
		}
		if d.IsDir() {
			if path != base && strings.HasPrefix(d.Name(), ".") {
				return fs.SkipDir
			}
			return nil
		}
		if !strings.EqualFold(filepath.Ext(d.Name()), ".md") {
			return nil
		}
		info, err := d.Info()
		if err != nil {
			return err
		}
		text, err := readHead(path, maxDocBytes)
		if err != nil {
			return err
		}
		rp, err := filepath.Rel(root, path)
		if err != nil {
			return err
		}
		docs = append(docs, DocText{Path: filepath.ToSlash(rp), Bytes: int(info.Size()), Text: text})
		return nil
	})
	return docs, err
}

// readHead reads at most max bytes of the file at path.
func readHead(path string, max int) (string, error) {
	f, err := os.Open(path)
	if err != nil {
		return "", err
	}
	defer f.Close()
	data, err := io.ReadAll(io.LimitReader(f, int64(max)))
	return string(data), err
}

// docOf builds the report's entry for a document: its title (the first
// heading, else the first line), the first MaxDocLines non-blank lines and,
// given a query, the first MaxDocLines lines that share a word with it.
func docOf(d DocText, query map[string]bool) Doc {
	out := Doc{Path: d.Path, Bytes: d.Bytes, Head: []string{}}
	for _, line := range strings.Split(strings.ReplaceAll(d.Text, "\r\n", "\n"), "\n") {
		t := strings.TrimSpace(line)
		if t == "" {
			continue
		}
		if out.Title == "" {
			if _, h := headingOf(line); h != "" {
				out.Title = clip(h, MaxItemRunes)
			}
		}
		if len(out.Head) < MaxDocLines {
			out.Head = append(out.Head, clip(t, MaxItemRunes))
			continue
		}
		if len(query) > 0 && len(out.Matches) < MaxDocLines && shares(query, t) > 0 {
			out.Matches = append(out.Matches, clip(t, MaxItemRunes))
		}
	}
	if out.Title == "" && len(out.Head) > 0 {
		out.Title = out.Head[0]
	}
	return out
}
