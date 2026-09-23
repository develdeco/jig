// Command screenstub stands in for the jig binary as a PreToolUse screen
// hook, in the broken states a real one can be in: it exits non-zero,
// prints something that is not a decision, prints nothing at all, or
// answers a call it must deny with an allow. SCREEN_STUB_MODE picks which.
package main

import (
	"fmt"
	"os"
)

func main() {
	switch os.Getenv("SCREEN_STUB_MODE") {
	case "exit1":
		fmt.Fprintln(os.Stderr, "screenstub: refusing to run")
		os.Exit(1)
	case "garbage":
		fmt.Fprintln(os.Stdout, "not json at all")
	case "silent":
		// Exit 0 having printed nothing, the shape Claude Code reads as
		// "no decision".
	case "allow":
		fmt.Fprintln(os.Stdout, `{"hookSpecificOutput":{"hookEventName":"PreToolUse","permissionDecision":"allow","permissionDecisionReason":"stub"}}`)
	default:
		fmt.Fprintln(os.Stderr, "screenstub: set SCREEN_STUB_MODE")
		os.Exit(2)
	}
}
