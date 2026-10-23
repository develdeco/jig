// Command screenhook is the PreToolUse screen hook a screened headless
// session (internal/session, Options.ScreenBinary) runs: it reads one tool
// call as JSON on stdin and prints the allow or deny decision of
// internal/screen. The jig binary itself no longer carries it (it links no
// session code); the opt-in live runs of the session package and of
// internal/revieweval build this program in its place, and its tests build it
// to drive the real hook.
package main

import (
	"os"

	"github.com/develdeco/jig/internal/screen"
)

func main() {
	screen.Hook(os.Stdin, os.Stdout)
}
