package store

import (
	"bytes"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"gopkg.in/yaml.v3"
)

// Intent is the parsed content of a ticket's intent.md: a human's own
// statement of what a change is meant to accomplish, recorded with the
// provenance its front matter names. Only "explicit" (written by `jig gate
// --intent`/`--doc`) is ever recorded today; inference (source "inferred")
// is a planned addition, not yet built.
type Intent struct {
	Source string
	Text   string
}

type intentFrontMatter struct {
	Source string `yaml:"source"`
}

// IntentPath returns ticket's intent.md path, rooted at Root.
func (s *Store) IntentPath(ticket string) string {
	return filepath.Join(s.TicketDir(ticket), "intent.md")
}

// WriteIntent writes ticket's intent.md, replacing one already there.
func (s *Store) WriteIntent(ticket string, in Intent) error {
	path := s.IntentPath(ticket)
	release, _, err := Lock(path, 30*time.Second)
	if err != nil {
		return err
	}
	defer release()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return fmt.Errorf("store: create ticket dir: %w", err)
	}
	return AtomicWrite(path, []byte(renderIntent(in)))
}

func renderIntent(in Intent) string {
	fm := intentFrontMatter{Source: in.Source}
	fmYAML, _ := yaml.Marshal(fm)
	var b strings.Builder
	b.WriteString("---\n")
	b.Write(fmYAML)
	b.WriteString("---\n")
	b.WriteString(in.Text)
	if !strings.HasSuffix(in.Text, "\n") {
		b.WriteString("\n")
	}
	return b.String()
}

// ReadIntent reads ticket's intent.md. ok is false when it does not exist;
// any other read or parse failure is returned as an error.
func (s *Store) ReadIntent(ticket string) (Intent, bool, error) {
	data, err := os.ReadFile(s.IntentPath(ticket))
	if err != nil {
		if os.IsNotExist(err) {
			return Intent{}, false, nil
		}
		return Intent{}, false, err
	}
	in, err := ParseIntent(data)
	if err != nil {
		return Intent{}, false, err
	}
	return in, true, nil
}

// ParseIntent parses intent.md's exact on-disk shape (a YAML front matter
// block, then the intent text) from data already read off disk. Exported so
// a caller that must read the file itself - verifydeliver's resolveIntent,
// to hash the same bytes it validates rather than reading the file a second
// time - can parse those same bytes instead of going through ReadIntent's
// own read.
func ParseIntent(data []byte) (Intent, error) {
	text := strings.ReplaceAll(string(data), "\r\n", "\n")
	if !strings.HasPrefix(text, "---\n") {
		return Intent{}, fmt.Errorf("store: intent.md: missing front matter")
	}
	rest := text[len("---\n"):]
	end := strings.Index(rest, "\n---\n")
	if end == -1 {
		return Intent{}, fmt.Errorf("store: intent.md: unterminated front matter")
	}
	fmText := rest[:end]
	body := strings.TrimPrefix(rest[end+len("\n---\n"):], "\n")

	// Strict: an unknown front-matter key is refused rather than silently
	// dropped, the same standard revieweval already holds its own files to
	// (yamlKnownFields).
	var fm intentFrontMatter
	dec := yaml.NewDecoder(bytes.NewReader([]byte(fmText)))
	dec.KnownFields(true)
	if err := dec.Decode(&fm); err != nil {
		return Intent{}, fmt.Errorf("store: intent.md: %w", err)
	}
	return Intent{Source: fm.Source, Text: strings.TrimRight(body, "\n")}, nil
}
