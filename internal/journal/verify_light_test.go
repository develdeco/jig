package journal

import "testing"

// TestVerificationStateKeepsALightPass: a light oracle line carries ran and
// reused_from through to the verification, the verified line that follows it
// keeps them, and a full run has no light words.
func TestVerificationStateKeepsALightPass(t *testing.T) {
	t.Parallel()
	full := Line{Slice: "a", Event: EventOracle, Outcome: "pass", Commit: "c1", Attempt: 1, Seconds: 90}
	light := Line{Slice: "a", Event: EventOracle, Outcome: "pass", Commit: "c2", Attempt: 2, Seconds: 3, Ran: "go test ./lint", ReusedFrom: "0123456789abcdef0123"}
	verified := Line{Slice: "a", Event: EventVerified, Commit: "c2", Attempt: 2}

	v := VerificationState([]Line{full, light, verified}, "c2", "a")
	if v.State != VerifyPass || v.Seconds != 3 || v.Ran != "go test ./lint" || v.ReusedFrom != light.ReusedFrom {
		t.Errorf("Verification = %+v", v)
	}
	if got, want := v.LightWords(), "lint only, reusing 0123456789ab"; got != want {
		t.Errorf("LightWords = %q, want %q", got, want)
	}
	if got := VerificationState([]Line{full}, "c1", "a").LightWords(); got != "" {
		t.Errorf("a full run's LightWords = %q, want none", got)
	}
	failed := light
	failed.Outcome = "fail"
	if got := VerificationState([]Line{failed}, "c2", "a").LightWords(); got != "" {
		t.Errorf("a failed light run's LightWords = %q, want none", got)
	}
}
