package store

import (
	"os"
	"time"
)

// readFile is os.ReadFile that rides out the moment AtomicWrite replaces the
// file. On Windows a reader that opens the file while the rename runs fails
// (a sharing violation, or access denied while the old file is pending
// delete), so such a failure is retried 10 times with a 50ms backoff, as
// AtomicWrite retries its rename. Elsewhere the rename is atomic for readers
// and replacing reports no error as transient.
func readFile(path string) ([]byte, error) {
	for attempt := 0; ; attempt++ {
		data, err := os.ReadFile(path)
		if err == nil || attempt == 9 || !replacing(err) {
			return data, err
		}
		time.Sleep(50 * time.Millisecond)
	}
}
