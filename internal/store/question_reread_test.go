package store

import (
	"sync"
	"testing"
)

// TestReadQuestionWhileItIsRewritten: a question read while another writer
// replaces its file (a person answering while a session waits on it) reads
// whole, never failing in the moment the replacement is renamed into place.
func TestReadQuestionWhileItIsRewritten(t *testing.T) {
	st := &Store{Root: t.TempDir()}
	q := Question{ID: "q-001", Slice: "c", Status: "open", Body: "Formal or casual?"}
	if err := st.WriteQuestion("JIG-1", q); err != nil {
		t.Fatal(err)
	}

	stop := make(chan struct{})
	var writeErr error
	var wg sync.WaitGroup
	wg.Add(1)
	go func() {
		defer wg.Done()
		for i := 0; i < 100; i++ {
			select {
			case <-stop:
				return
			default:
			}
			if err := st.WriteQuestion("JIG-1", q); err != nil {
				writeErr = err
				return
			}
		}
	}()
	writing := make(chan struct{})
	go func() { wg.Wait(); close(writing) }()

	reads := 0
	var readErr error
read:
	for {
		select {
		case <-writing:
			break read
		default:
		}
		got, err := st.ReadQuestion("JIG-1", "q-001")
		if err == nil && got != q {
			t.Errorf("read %d = %+v, want %+v", reads, got, q)
		}
		if err == nil {
			_, err = st.ReadQuestions("JIG-1")
		}
		if err != nil {
			readErr = err
			break
		}
		reads++
	}
	close(stop)
	<-writing
	if readErr != nil {
		t.Errorf("read %d while the question was rewritten: %v", reads, readErr)
	}
	if writeErr != nil {
		t.Errorf("rewrite while the question was read: %v", writeErr)
	}
	if reads == 0 {
		t.Error("no read ran while the question was rewritten")
	}
}
