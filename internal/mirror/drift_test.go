package mirror

import (
	"os"
	"testing"

	"gopkg.in/yaml.v3"
)

func itoa(n int) string {
	if n == 0 {
		return "0"
	}
	neg := n < 0
	if neg {
		n = -n
	}
	var digits []byte
	for n > 0 {
		digits = append([]byte{byte('0' + n%10)}, digits...)
		n /= 10
	}
	if neg {
		digits = append([]byte{'-'}, digits...)
	}
	return string(digits)
}

func readTestRecord(t *testing.T, abs string) githubRecord {
	t.Helper()
	data, err := os.ReadFile(abs)
	if err != nil {
		t.Fatal(err)
	}
	var rec githubRecord
	if err := yaml.Unmarshal(data, &rec); err != nil {
		t.Fatal(err)
	}
	return rec
}
