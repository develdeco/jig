// Command herdrstub is a fake `herdr` binary used only by session's herdr
// backend tests. It never drives a real agent: it records every
// invocation's argv to $HERDR_STUB_LOG (one JSON array per line) and prints
// the canned JSON response herdrBackend.Run expects for each control
// command in a full Run: workspace create, agent start, agent prompt,
// agent read, and workspace close. Two environment variables make a command
// misbehave, each naming it as "noun verb" ("agent prompt"):
// HERDR_STUB_FAIL_CMD exits 1 after writing a line to stderr that does not
// echo the command's operands, HERDR_STUB_SILENT_FAIL_CMD exits 1 with nothing
// on stderr, and HERDR_STUB_GARBAGE_CMD prints a response that is not JSON.
package main

import (
	"encoding/json"
	"fmt"
	"os"
	"strings"
)

func main() {
	logArgs(os.Getenv("HERDR_STUB_LOG"), os.Args)

	args := os.Args[1:]

	sub := subcommand(args)
	if sub != "" && sub == os.Getenv("HERDR_STUB_FAIL_CMD") {
		fmt.Fprintln(os.Stderr, "herdr stub: the command was refused")
		os.Exit(1)
	}
	if sub != "" && sub == os.Getenv("HERDR_STUB_SILENT_FAIL_CMD") {
		os.Exit(1)
	}
	if sub != "" && sub == os.Getenv("HERDR_STUB_GARBAGE_CMD") {
		fmt.Println("this is not json")
		return
	}

	switch {
	case len(args) >= 2 && args[0] == "workspace" && args[1] == "create":
		fmt.Println(`{"result":{"workspace":"ws-1","root_pane":{"pane_id":"pane-1"}}}`)
	case len(args) >= 2 && args[0] == "agent" && args[1] == "start":
		fmt.Println(`{"result":{}}`)
	case len(args) >= 2 && args[0] == "agent" && args[1] == "prompt":
		fmt.Println(`{"result":{"state":"done"}}`)
	case len(args) >= 2 && args[0] == "agent" && args[1] == "read":
		printReadResult()
	case len(args) >= 2 && args[0] == "workspace" && args[1] == "close":
		fmt.Println(`{"result":{}}`)
	default:
		fmt.Println("{}")
	}
}

// subcommand names the herdr command args make, "noun verb": args are herdr's
// own, or the ones herdrBackend hands wsl on Windows ("-e bash -lc" and a
// command line whose words are each single-quoted).
func subcommand(args []string) string {
	if len(args) == 4 && args[0] == "-e" && args[1] == "bash" && args[2] == "-lc" {
		words := strings.Fields(strings.TrimPrefix(args[3], "herdr "))
		args = nil
		for _, w := range words {
			args = append(args, strings.Trim(w, "'"))
		}
	}
	if len(args) < 2 {
		return ""
	}
	return args[0] + " " + args[1]
}

// printReadResult prints the `agent read` response: a result whose text
// carries the single fenced ```json result block outcome.ParseText expects,
// so herdrBackend.Run's fallback-read path parses a green result from it.
func printReadResult() {
	fenced := "```json\n" + `{"outcome":"green","summary":"herdr stub done"}` + "\n```"
	out, err := json.Marshal(map[string]any{"result": map[string]any{"text": fenced}})
	if err != nil {
		return
	}
	fmt.Println(string(out))
}

func logArgs(path string, args []string) {
	if path == "" {
		return
	}
	data, err := json.Marshal(args)
	if err != nil {
		return
	}
	f, err := os.OpenFile(path, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o644)
	if err != nil {
		return
	}
	defer f.Close()
	fmt.Fprintln(f, string(data))
}
