package spend

import "testing"

// TestParseAndFormat: counts as a person writes them, dollars with or without
// the sign, and the short form a note uses.
func TestParseAndFormat(t *testing.T) {
	t.Parallel()
	for in, want := range map[string]int64{"5000000": 5_000_000, "5M": 5_000_000, "1.5m": 1_500_000, "200k": 200_000, " 2G ": 2_000_000_000, "0": 0} {
		if got, err := ParseTokens(in); err != nil || got != want {
			t.Errorf("ParseTokens(%q) = %d, %v, want %d", in, got, err, want)
		}
	}
	for _, bad := range []string{"", "abc", "-5", "5x", "NaN", "1e30"} {
		if got, err := ParseTokens(bad); err == nil {
			t.Errorf("ParseTokens(%q) = %d, want an error", bad, got)
		}
	}
	for in, want := range map[string]float64{"20": 20, "12.50": 12.5, "$3": 3, "0": 0} {
		if got, err := ParseUSD(in); err != nil || got != want {
			t.Errorf("ParseUSD(%q) = %v, %v, want %v", in, got, err, want)
		}
	}
	for _, bad := range []string{"", "x", "-1", "$-1", "Inf"} {
		if got, err := ParseUSD(bad); err == nil {
			t.Errorf("ParseUSD(%q) = %v, want an error", bad, got)
		}
	}
	for n, want := range map[int64]string{0: "0", 950: "950", 1000: "1k", 1234: "1.2k", 31_200_000: "31.2M", 5_000_000: "5M", 2_000_000_000: "2G"} {
		if got := Compact(n); got != want {
			t.Errorf("Compact(%d) = %q, want %q", n, got, want)
		}
	}
	if got := Dollars(12.5); got != "$12.50" {
		t.Errorf("Dollars(12.5) = %q", got)
	}
}
