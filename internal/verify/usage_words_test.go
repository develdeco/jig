package verify

import "testing"

// TestUsageIsToldInWords: CPU in seconds with one decimal, memory in MiB or
// GiB, the basis in words, and nothing for a figure that is not there.
func TestUsageIsToldInWords(t *testing.T) {
	t.Parallel()
	for _, c := range []struct {
		got, want string
	}{
		{CPUWords(63200), "63.2"},
		{CPUWords(1), "0.0"},
		{CPUWords(0), ""},
		{MemoryWords(300 << 20), "300 MiB"},
		{MemoryWords(1), "1 MiB"},
		{MemoryWords(1536 << 20), "1.5 GiB"},
		{MemoryWords(0), ""},
		{BasisWords("job"), "job"},
		{BasisWords("sampled"), "sampled peak"},
	} {
		if c.got != c.want {
			t.Errorf("got %q, want %q", c.got, c.want)
		}
	}
}
