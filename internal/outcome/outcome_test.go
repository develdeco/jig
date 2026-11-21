package outcome

import "testing"

func TestSignature(t *testing.T) {
	a := Signature("slice", CodeBug, "failed in src/app/main.go line 42")
	b := Signature("slice", CodeBug, "failed in lib/other.rs line 7")
	if a != b {
		t.Fatalf("path/digit normalization mismatch: %q != %q", a, b)
	}
	if a != "slice|code-bug|failed in line" {
		t.Fatalf("Signature = %q, want %q", a, "slice|code-bug|failed in line")
	}

	// Case folding.
	c := Signature("slice", Failed, "SAME FAILURE")
	d := Signature("slice", Failed, "same failure")
	if c != d {
		t.Fatalf("case folding mismatch: %q != %q", c, d)
	}

	// 160-char truncation: two summaries differing only after char 160
	// must yield the same gist.
	base := ""
	for i := 0; i < 160; i++ {
		base += "a"
	}
	e := Signature("slice", Failed, base+"tail-one")
	f := Signature("slice", Failed, base+"tail-two")
	if e != f {
		t.Fatalf("truncation mismatch: %q != %q", e, f)
	}
}
