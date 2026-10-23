package spend

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// TestRatesLookupNormalizesTheModelID: a snapshot date, a context window
// suffix and the latest alias are all the same model, and a model of a family
// the table does not list is not guessed at.
func TestRatesLookupNormalizesTheModelID(t *testing.T) {
	t.Parallel()
	opus45 := Rates{5, 25, 6.25, 10, 0.50}
	for _, model := range []string{"claude-opus-4-5", "claude-opus-4-5-20251101", "CLAUDE-OPUS-4-5[1m]", " claude-opus-4-5-latest "} {
		if r, ok := (Prices(nil)).Lookup(model); !ok || r != opus45 {
			t.Errorf("Lookup(%q) = %+v, %v, want opus 4.5 rates", model, r, ok)
		}
	}
	if r, ok := (Prices(nil)).Lookup("claude-opus-4-20250514"); !ok || r.Input != 15 {
		t.Errorf("Lookup(claude-opus-4-20250514) = %+v, %v, want the opus 4 rates (15 in)", r, ok)
	}
	for _, model := range []string{"", "claude-fable-5", "claude-opus-4-9", "gpt-5", "<synthetic>"} {
		if r, ok := (Prices(nil)).Lookup(model); ok {
			t.Errorf("Lookup(%q) = %+v, want no price: a model the table does not list is reported, not guessed", model, r)
		}
	}
}

// TestPricesOverrideTheBuiltInOnes: a price of the jig home beats the table.
func TestPricesOverrideTheBuiltInOnes(t *testing.T) {
	t.Parallel()
	p := Prices{"claude-opus-4-5": {Input: 1, Output: 2}}
	if r, _ := p.Lookup("claude-opus-4-5-20251101"); r.Input != 1 || r.Output != 2 {
		t.Errorf("rates = %+v, want the override", r)
	}
	if r, ok := p.Lookup("claude-3-haiku-20240307"); !ok || r.Input != 0.25 {
		t.Errorf("rates = %+v, %v, want the built-in haiku 3 rates for a model the override does not name", r, ok)
	}
}

// TestLoadPrices: the file maps model ids to rates; the cache rates default
// to the usual multiples of the input rate; a missing file adds nothing and a
// file that cannot be used is an error.
func TestLoadPrices(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	if p, err := LoadPrices(dir); err != nil || p != nil {
		t.Fatalf("no file: prices = %v, err = %v, want none and no error", p, err)
	}
	write := func(body string) {
		t.Helper()
		if err := os.WriteFile(filepath.Join(dir, PricesFile), []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	write("claude-fable-5-20260101:\n  input: 10\n  output: 50\nclaude-x:\n  input: 2\n  output: 4\n  cache_write: 3\n  cache_write_1h: 5\n  cache_read: 0.1\n")
	p, err := LoadPrices(dir)
	if err != nil {
		t.Fatal(err)
	}
	fable, ok := p.Lookup("claude-fable-5")
	if !ok || fable != (Rates{10, 50, 12.5, 20, 1}) {
		t.Errorf("fable rates = %+v, %v, want 10/50 with cache write 12.5 (5m), 20 (1h) and read 1", fable, ok)
	}
	if x, _ := p.Lookup("claude-x"); x != (Rates{2, 4, 3, 5, 0.1}) {
		t.Errorf("claude-x rates = %+v, want the ones the file names", x)
	}

	for body, want := range map[string]string{
		"claude-y:\n  input: 1\n": "needs an input and an output price",
		"claude-y: [1, 2":         "parse",
	} {
		write(body)
		if _, err := LoadPrices(dir); err == nil || !strings.Contains(err.Error(), want) {
			t.Errorf("file %q: err = %v, want one mentioning %q", body, err, want)
		}
	}
}
