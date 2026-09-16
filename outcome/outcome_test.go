package outcome

import "testing"

func TestParseTable(t *testing.T) {
	cases := []struct {
		name    string
		text    string
		wantOut string
		wantSum string
	}{
		{
			name:    "green result parses",
			text:    "session log\n```json\n{\"outcome\":\"green\",\"summary\":\"12/12 passed\"}\n```\n",
			wantOut: Green,
			wantSum: "12/12 passed",
		},
		{
			name:    "prose-only fails",
			text:    "I think it's fine, nothing more to say.",
			wantOut: Failed,
			wantSum: "no result block",
		},
		{
			name:    "malformed json fails",
			text:    "```json\n{oops\n```",
			wantOut: Failed,
		},
		{
			name:    "wrong-context outcome fails",
			text:    "```json\n{\"outcome\":\"done\",\"summary\":\"finished\"}\n```",
			wantOut: Failed,
		},
		{
			name:    "two blocks fails (jig deviation)",
			text:    "```json\n{\"outcome\":\"code-bug\",\"summary\":\"a\"}\n```\nmore text\n```json\n{\"outcome\":\"green\",\"summary\":\"b\"}\n```",
			wantOut: Failed,
			wantSum: "multiple result blocks",
		},
		{
			name:    "one block green parses",
			text:    "```json\n{\"outcome\":\"green\",\"summary\":\"ok\",\"commit\":\"deadbeef\"}\n```",
			wantOut: Green,
			wantSum: "ok",
		},
	}

	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			res := ParseText("slice", c.text)
			if res.Outcome != c.wantOut {
				t.Fatalf("Outcome = %q, want %q", res.Outcome, c.wantOut)
			}
			if c.wantSum != "" && res.Summary != c.wantSum {
				t.Fatalf("Summary = %q, want %q", res.Summary, c.wantSum)
			}
		})
	}
}

func TestParseJSONDirect(t *testing.T) {
	t.Run("malformed json names the error", func(t *testing.T) {
		res := ParseJSON("slice", []byte("{oops"))
		if res.Outcome != Failed {
			t.Fatalf("Outcome = %q, want failed", res.Outcome)
		}
		if res.Summary == "" {
			t.Fatal("expected non-empty summary explaining the JSON error")
		}
	})

	t.Run("invalid outcome names the bad outcome", func(t *testing.T) {
		res := ParseJSON("slice", []byte(`{"outcome":"done","summary":"x"}`))
		if res.Outcome != Failed {
			t.Fatalf("Outcome = %q, want failed", res.Outcome)
		}
		if res.Summary == "" {
			t.Fatal("expected non-empty summary naming the bad outcome")
		}
	})

	t.Run("valid outcome populates result", func(t *testing.T) {
		res := ParseJSON("slice", []byte(`{"outcome":"needs-input","summary":"pick one","question":"a or b?"}`))
		if res.Outcome != NeedsInput {
			t.Fatalf("Outcome = %q, want needs-input", res.Outcome)
		}
		if res.Question != "a or b?" {
			t.Fatalf("Question = %q", res.Question)
		}
	})
}

func TestParseTextRawTail(t *testing.T) {
	long := ""
	for i := 0; i < 2000; i++ {
		long += "x"
	}
	text := long + "\n```json\n{\"outcome\":\"green\",\"summary\":\"ok\"}\n```"
	res := ParseText("slice", text)
	if len(res.RawTail) != 1200 {
		t.Fatalf("RawTail len = %d, want 1200", len(res.RawTail))
	}
	if res.RawTail != text[len(text)-1200:] {
		t.Fatal("RawTail is not the last 1200 characters of text")
	}
}

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

func TestStallCounter(t *testing.T) {
	t.Run("same signature twice stalls", func(t *testing.T) {
		var c StallCounter
		sig := "slice|code-bug|same failure"
		r1, s1 := c.Observe(sig, false)
		if r1 != 1 || s1 {
			t.Fatalf("first observe = (%d,%v), want (1,false)", r1, s1)
		}
		r2, s2 := c.Observe(sig, false)
		if r2 != 2 || !s2 {
			t.Fatalf("second observe = (%d,%v), want (2,true)", r2, s2)
		}
	})

	t.Run("success never counts", func(t *testing.T) {
		var c StallCounter
		sig := "slice|green|ok"
		for i := 0; i < 5; i++ {
			r, s := c.Observe(sig, true)
			if r != 0 || s {
				t.Fatalf("observe %d = (%d,%v), want (0,false)", i, r, s)
			}
		}
	})

	t.Run("different signatures do not stall each other", func(t *testing.T) {
		var c StallCounter
		r1, s1 := c.Observe("slice|code-bug|a", false)
		r2, s2 := c.Observe("slice|code-bug|b", false)
		if r1 != 1 || s1 || r2 != 1 || s2 {
			t.Fatalf("got (%d,%v) (%d,%v), want (1,false) (1,false)", r1, s1, r2, s2)
		}
	})

	t.Run("interleaved success does not reset counts", func(t *testing.T) {
		var c StallCounter
		sig := "slice|code-bug|same failure"
		c.Observe(sig, false)
		c.Observe("slice|green|ok", true)
		r, s := c.Observe(sig, false)
		if r != 2 || !s {
			t.Fatalf("Observe after interleaved success = (%d,%v), want (2,true)", r, s)
		}
	})
}
