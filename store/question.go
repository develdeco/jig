package store

import (
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"time"

	"gopkg.in/yaml.v3"
)

// Question is one entry of a ticket's questions/<id>.md.
type Question struct {
	ID     string
	Slice  string
	Status string // open|answered
	Body   string
	Answer string
}

type questionFrontMatter struct {
	ID     string `yaml:"id"`
	Slice  string `yaml:"slice"`
	Status string `yaml:"status"`
}

func (s *Store) questionsDir(ticket string) string {
	return filepath.Join(s.TicketDir(ticket), "questions")
}

func (s *Store) questionPath(ticket, id string) string {
	return filepath.Join(s.questionsDir(ticket), id+".md")
}

var questionIDRE = regexp.MustCompile(`^q-(\d+)\.md$`)

// NextQuestionID returns the next unused question id (q-001, q-002, ...)
// for ticket.
func (s *Store) NextQuestionID(ticket string) string {
	entries, err := os.ReadDir(s.questionsDir(ticket))
	max := 0
	if err == nil {
		for _, e := range entries {
			m := questionIDRE.FindStringSubmatch(e.Name())
			if m == nil {
				continue
			}
			if n, err := strconv.Atoi(m[1]); err == nil && n > max {
				max = n
			}
		}
	}
	return fmt.Sprintf("q-%03d", max+1)
}

// WriteQuestion writes q as markdown with a YAML front-matter header,
// appending an "## Answer" section when answered.
func (s *Store) WriteQuestion(ticket string, q Question) error {
	path := s.questionPath(ticket, q.ID)
	release, _, err := Lock(path, 30*time.Second)
	if err != nil {
		return err
	}
	defer release()
	return AtomicWrite(path, []byte(renderQuestion(q)))
}

func renderQuestion(q Question) string {
	fm := questionFrontMatter{ID: q.ID, Slice: q.Slice, Status: q.Status}
	fmYAML, _ := yaml.Marshal(fm)
	var b strings.Builder
	b.WriteString("---\n")
	b.Write(fmYAML)
	b.WriteString("---\n")
	b.WriteString(q.Body)
	if !strings.HasSuffix(q.Body, "\n") {
		b.WriteString("\n")
	}
	// The answer text is preserved regardless of status, so a subsequent
	// Supersede does not erase a recorded answer.
	if q.Answer != "" {
		b.WriteString("\n## Answer\n\n")
		b.WriteString(q.Answer)
		if !strings.HasSuffix(q.Answer, "\n") {
			b.WriteString("\n")
		}
	}
	return b.String()
}

func parseQuestion(id string, data []byte) (Question, error) {
	text := strings.ReplaceAll(string(data), "\r\n", "\n")
	if !strings.HasPrefix(text, "---\n") {
		return Question{}, fmt.Errorf("store: question %s: missing front matter", id)
	}
	rest := text[len("---\n"):]
	end := strings.Index(rest, "\n---\n")
	if end == -1 {
		return Question{}, fmt.Errorf("store: question %s: unterminated front matter", id)
	}
	fmText := rest[:end]
	body := strings.TrimPrefix(rest[end+len("\n---\n"):], "\n")

	var fm questionFrontMatter
	if err := yaml.Unmarshal([]byte(fmText), &fm); err != nil {
		return Question{}, fmt.Errorf("store: question %s: %w", id, err)
	}

	answer := ""
	if idx := strings.Index(body, "\n## Answer\n"); idx != -1 {
		answer = strings.TrimSpace(body[idx+len("\n## Answer\n"):])
		body = body[:idx]
	}
	body = strings.TrimRight(body, "\n")

	return Question{
		ID:     fm.ID,
		Slice:  fm.Slice,
		Status: fm.Status,
		Body:   body,
		Answer: answer,
	}, nil
}

// ReadQuestions reads every question for ticket, sorted by id.
func (s *Store) ReadQuestions(ticket string) ([]Question, error) {
	entries, err := os.ReadDir(s.questionsDir(ticket))
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, err
	}
	var out []Question
	for _, e := range entries {
		m := questionIDRE.FindStringSubmatch(e.Name())
		if m == nil {
			continue
		}
		id := strings.TrimSuffix(e.Name(), ".md")
		data, err := os.ReadFile(s.questionPath(ticket, id))
		if err != nil {
			return nil, err
		}
		q, err := parseQuestion(id, data)
		if err != nil {
			return nil, err
		}
		out = append(out, q)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].ID < out[j].ID })
	return out, nil
}

// Answer marks question qid as answered with text and returns its slice so
// the caller can re-queue it. It fails if the question is not currently
// open (e.g. already answered or superseded), naming the current status.
func (s *Store) Answer(ticket, qid, text string) (slice string, err error) {
	path := s.questionPath(ticket, qid)
	release, _, err := Lock(path, 30*time.Second)
	if err != nil {
		return "", err
	}
	defer release()

	data, err := os.ReadFile(path)
	if err != nil {
		return "", err
	}
	q, err := parseQuestion(qid, data)
	if err != nil {
		return "", err
	}
	if q.Status != "open" {
		return "", fmt.Errorf("store: question %s: cannot answer: status is %q, not open", qid, q.Status)
	}
	q.Status = "answered"
	q.Answer = text
	if err := AtomicWrite(path, []byte(renderQuestion(q))); err != nil {
		return "", err
	}
	return q.Slice, nil
}

// Supersede marks question qid superseded — its slice was re-queued by a
// brief amendment, so the question no longer counts as open.
func (s *Store) Supersede(ticket, qid string) error {
	path := s.questionPath(ticket, qid)
	release, _, err := Lock(path, 30*time.Second)
	if err != nil {
		return err
	}
	defer release()

	data, err := os.ReadFile(path)
	if err != nil {
		return err
	}
	q, err := parseQuestion(qid, data)
	if err != nil {
		return err
	}
	q.Status = "superseded"
	return AtomicWrite(path, []byte(renderQuestion(q)))
}
