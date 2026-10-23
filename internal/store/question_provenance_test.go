package store

import (
	"os"
	"strings"
	"sync"
	"testing"
)

// TestAnswerAsRecordsWhoTheAnswerSpeaksFor: the provenance rides in the
// question's file, survives a supersede, and a question answered without one
// reads back with none (the file an older jig wrote).
func TestAnswerAsRecordsWhoTheAnswerSpeaksFor(t *testing.T) {
	st := &Store{Root: t.TempDir()}
	if err := st.WriteQuestion("JIG-1", Question{ID: "q-001", Slice: "c", Status: "open", Body: "Formal or casual?"}); err != nil {
		t.Fatal(err)
	}
	if err := st.WriteQuestion("JIG-1", Question{ID: "q-002", Slice: "c", Status: "open", Body: "Which locale?"}); err != nil {
		t.Fatal(err)
	}

	if slice, err := st.AnswerAs("JIG-1", "q-001", "Casual.", "person"); err != nil || slice != "c" {
		t.Fatalf("AnswerAs = %q, %v, want c and no error", slice, err)
	}
	if _, err := st.Answer("JIG-1", "q-002", "en-US."); err != nil {
		t.Fatal(err)
	}

	got, err := st.ReadQuestion("JIG-1", "q-001")
	if err != nil {
		t.Fatal(err)
	}
	want := Question{ID: "q-001", Slice: "c", Status: "answered", Body: "Formal or casual?", Answer: "Casual.", Provenance: "person"}
	if got != want {
		t.Fatalf("ReadQuestion = %+v, want %+v", got, want)
	}
	if plain, err := st.ReadQuestion("JIG-1", "q-002"); err != nil || plain.Provenance != "" || plain.Answer != "en-US." {
		t.Fatalf("a question answered with Answer = %+v, %v, want an answer and no provenance", plain, err)
	}

	if err := st.Supersede("JIG-1", "q-001"); err != nil {
		t.Fatal(err)
	}
	if after, _ := st.ReadQuestion("JIG-1", "q-001"); after.Provenance != "person" || after.Status != "superseded" {
		t.Fatalf("after Supersede = %+v, want the provenance kept", after)
	}

	data, err := os.ReadFile(st.questionPath("JIG-1", "q-002"))
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(data), "provenance") {
		t.Errorf("a question with no provenance writes none:\n%s", data)
	}
}

// TestReadQuestionNamesAMissingOne: a question that is not there is an
// os.IsNotExist error, so a waiter can tell it from a broken file.
func TestReadQuestionNamesAMissingOne(t *testing.T) {
	st := &Store{Root: t.TempDir()}
	if _, err := st.ReadQuestion("JIG-1", "q-009"); !os.IsNotExist(err) {
		t.Fatalf("ReadQuestion of a missing question = %v, want a not-exist error", err)
	}
}

// TestAddQuestionAllocatesDistinctIDs: questions added at once, each with the
// whole id-and-write step to itself, get distinct ids and all of them are on
// disk, in order.
func TestAddQuestionAllocatesDistinctIDs(t *testing.T) {
	st := &Store{Root: t.TempDir()}
	const n = 12
	var wg sync.WaitGroup
	ids := make([]string, n)
	errs := make([]error, n)
	for i := 0; i < n; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			q, err := st.AddQuestion("JIG-1", "a", "question")
			ids[i], errs[i] = q.ID, err
		}(i)
	}
	wg.Wait()

	seen := map[string]bool{}
	for i := range ids {
		if errs[i] != nil {
			t.Fatalf("AddQuestion %d: %v", i, errs[i])
		}
		if seen[ids[i]] {
			t.Errorf("id %s was handed out twice", ids[i])
		}
		seen[ids[i]] = true
	}
	qs, err := st.ReadQuestions("JIG-1")
	if err != nil {
		t.Fatal(err)
	}
	if len(qs) != n || qs[0].ID != "q-001" || qs[n-1].ID != "q-012" {
		t.Fatalf("ReadQuestions = %d questions from %s to %s, want %d from q-001 to q-012", len(qs), qs[0].ID, qs[len(qs)-1].ID, n)
	}
	for _, q := range qs {
		if q.Status != "open" || q.Slice != "a" {
			t.Errorf("question %+v, want open and about slice a", q)
		}
	}
}

// TestBriefSectionsKeepBodiesAndHashes: BriefSections yields the sections in
// order with the body each hash covers, and BriefSectionHashes is the same
// hashes by heading.
func TestBriefSectionsKeepBodiesAndHashes(t *testing.T) {
	brief := []byte("# Title\n\nintro\n\n## Goal\r\n\r\nDo it.\r\n\n## Seams\n\n- the verb\n\n")
	got := BriefSections(brief)
	if len(got) != 2 || got[0].Heading != "Goal" || got[1].Heading != "Seams" {
		t.Fatalf("BriefSections = %+v, want Goal then Seams", got)
	}
	if got[0].Body != "\nDo it." || got[1].Body != "\n- the verb" {
		t.Errorf("bodies = %q and %q, want them with line endings normalized and trailing space trimmed", got[0].Body, got[1].Body)
	}
	hashes := BriefSectionHashes(brief)
	if hashes["Goal"] != got[0].Hash || hashes["Seams"] != got[1].Hash || len(hashes) != 2 {
		t.Errorf("BriefSectionHashes = %v, want the hashes BriefSections holds", hashes)
	}
	if BriefSections([]byte("no sections here\n")) != nil {
		t.Error("a brief with no \"## \" heading has sections")
	}
}
