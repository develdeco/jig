package main

import (
	"encoding/json"
	"fmt"
	"io"
	"strconv"
	"strings"

	"github.com/develdeco/jig/internal/axi"
	"github.com/develdeco/jig/internal/journal"
	"github.com/develdeco/jig/internal/verify"
)

// finalRequestOf is the final run the parsed command asks for: a final run is
// of a ticket, so the request carries no slice.
func finalRequestOf(req verify.Request) verify.FinalRequest {
	return verify.FinalRequest{
		Ticket: req.Ticket, Session: req.Session, Agent: req.Agent, Flags: req.Flags, Timeout: req.Timeout, Dir: req.Dir, NoRecord: req.NoRecord,
	}
}

// runFinal is `jig verify --final`: the union of the ticket's slice commands
// run in this process, in the foreground, between a header and a footer, and
// recorded as one final-oracle line. The exit code is the command's own (125
// when the union could not be reached, 1 for a run jig stopped). --dry-run
// prints the header and the record that would be written and runs and writes
// nothing.
func runFinal(d verify.Deps, req verify.Request, o verifyOpts, stream, stdout io.Writer) int {
	fr := finalRequestOf(req)
	plan, err := verify.PlanFinal(d, fr)
	if err != nil {
		return unrun(stdout, err)
	}
	axi.Render(stream, verifyFinalHeader(plan, req, o)...)
	if o.dryRun {
		axi.Render(stream, verifyFinalWouldRecord(plan, req)...)
		return 0
	}

	d.Out = stream
	res, err := verify.RunFinal(d, fr)
	if err != nil {
		return unrun(stdout, err)
	}
	if res.State == journal.VerifyError {
		axi.Render(stdout, axi.KV("error", [][2]string{{"message", res.Summary}}), axi.Help("Fix what the message says, then run `jig verify --final` again"))
		return verify.ExitUnrun
	}
	code := res.Code
	if res.State == "" {
		code = 1
	}
	res.Pinned = verify.PinnedText(d, res.Ticket, pinnedHead(res.Result))
	if o.json {
		out, err := json.Marshal(verifyFinalJSONOf(res, req, code))
		if err != nil {
			return unrun(stdout, err)
		}
		fmt.Fprintln(stdout, string(out))
		return code
	}
	axi.Render(stream, verifyFinalFooter(res, req, code)...)
	return code
}

// verifyFinalHeader is what jig prints before the union's output: the command
// as one line, the slices it is the union of with the command each
// contributes, the directory, the commit, the tree, and every flag with the
// default it has.
func verifyFinalHeader(plan verify.FinalResult, req verify.Request, o verifyOpts) []string {
	on := func(b bool) string {
		if b {
			return "on"
		}
		return "off"
	}
	timeout, timeoutDefault := "none", true
	if req.Timeout > 0 {
		timeout, timeoutDefault = req.Timeout.String(), false
	}
	kv := [][2]string{
		{"command", plan.Command + " (the union of the ticket's slice commands)"}, {"dir", plan.Dir}, {"commit", plan.SHA},
		{"ticket", plan.Ticket}, {"tree", plan.TreeBefore},
	}
	if len(plan.Classes) > 0 {
		kv = append(kv, [2]string{"env", strings.Join(plan.Classes, ",")})
	}
	if o.json {
		kv = append(kv, [2]string{"output", "stderr (--json)"})
	}
	rows := make([][]string, len(plan.Slices))
	for i, s := range plan.Slices {
		rows[i] = []string{s.ID, s.Command}
	}
	flags := [][2]string{
		flagLine("final", "on", false),
		flagLine("dir", plan.Dir, o.dir == ""),
		flagLine("timeout", timeout, timeoutDefault),
		flagLine("dry-run", on(o.dryRun), !o.dryRun),
		flagLine("no-record", on(o.noRecord), !o.noRecord),
		flagLine("json", on(o.json), !o.json),
	}
	return []string{axi.KV("verify", kv), axi.Table("slices", []string{"slice", "command"}, rows), axi.KV("flags", flags)}
}

// verifyFinalWouldRecord is what --dry-run prints after the header: the line a
// run would write, and what it would change.
func verifyFinalWouldRecord(plan verify.FinalResult, req verify.Request) []string {
	ids := make([]string, len(plan.Slices))
	for i, s := range plan.Slices {
		ids[i] = s.ID
	}
	record := [][2]string{
		{"event", journal.EventFinalOracle}, {"commit", plan.SHA}, {"command", plan.Command}, {"dir", plan.Dir}, {"flags", req.Flags},
		{"slices", strings.Join(ids, " ")}, {"tree_before", plan.TreeBefore},
	}
	covers := "yes"
	if plan.Uncovered != "" {
		covers = "no"
		record = append(record, [2]string{"uncovered", plan.Uncovered})
	}
	record = append(record, [2]string{"covers", covers})
	changes := "nothing but the final-oracle line: a final run changes no slice state and writes no verified line"
	if req.NoRecord {
		changes = "nothing: --no-record writes no line, state or push"
	}
	return []string{
		axi.KV("would_record", record),
		axi.KV("would_change", [][2]string{{"effect", changes}}),
		axi.Help("Nothing ran and nothing was written; run `jig verify --final` without --dry-run to run it"),
	}
}

// verifyFinalFooter is what jig prints after the union's output.
func verifyFinalFooter(res verify.FinalResult, req verify.Request, code int) []string {
	outcome := res.State
	if outcome == "" {
		outcome = "stopped"
	}
	covers := "yes"
	if !res.Covered {
		covers = "no"
	}
	kv := [][2]string{
		{"ticket", res.Ticket}, {"commit", res.SHA}, {"outcome", outcome}, {"exit", strconv.Itoa(code)}, {"seconds", strconv.Itoa(res.Seconds)},
		{"covers", covers}, {"head", shortSHA(res.HeadEnd)},
	}
	if res.Uncovered != "" {
		kv = append(kv, [2]string{"uncovered", res.Uncovered})
	}
	for _, f := range [][2]string{{"summary", res.Summary}, {"signature", res.Signature}} {
		if f[1] != "" {
			kv = append(kv, f)
		}
	}
	kv = append(kv, verifyRecorded(res.Result, req)...)
	blocks := []string{axi.KV("result", kv)}
	if res.PushErr != "" {
		blocks = append(blocks, axi.KV("warning", [][2]string{{"message", "the run is recorded here but could not be pushed to the store (the next checkpoint will): " + res.PushErr}}))
	}
	blocks = append(blocks, pinnedBlocks(res.Pinned)...)
	help := "Publish names this run when it is at the reviewed head"
	switch {
	case !res.Covered:
		help = "This run covers no commit: commit or discard the changes, then run `jig verify --final` again"
	case outcome == "fail":
		help = "The union failed: fix what it reports, commit, and run `jig verify --final` again at the new head"
	}
	return append(blocks, axi.Help(help))
}

// verifyFinalJSON is the closing report of a final run as --json prints it.
type verifyFinalJSON struct {
	Ticket     string   `json:"ticket"`
	Final      bool     `json:"final"`
	Slices     []string `json:"slices"`
	Commit     string   `json:"commit"`
	HeadEnd    string   `json:"head_end"`
	Command    string   `json:"command"`
	Dir        string   `json:"dir"`
	Outcome    string   `json:"outcome"`
	Exit       int      `json:"exit"`
	Seconds    int      `json:"seconds"`
	Covered    bool     `json:"covered"`
	Uncovered  string   `json:"uncovered,omitempty"`
	TreeBefore string   `json:"tree_before"`
	TreeAfter  string   `json:"tree_after"`
	Recorded   []string `json:"recorded"`
	NoRecord   bool     `json:"no_record,omitempty"`
	Pushed     bool     `json:"pushed"`
	PushError  string   `json:"push_error,omitempty"`
	Summary    string   `json:"summary,omitempty"`
	Signature  string   `json:"signature,omitempty"`
	pinnedJSON
}

func verifyFinalJSONOf(res verify.FinalResult, req verify.Request, code int) verifyFinalJSON {
	outcome := res.State
	if outcome == "" {
		outcome = "stopped"
	}
	recorded := res.Recorded
	if recorded == nil {
		recorded = []string{}
	}
	ids := make([]string, len(res.Slices))
	for i, s := range res.Slices {
		ids[i] = s.ID
	}
	return verifyFinalJSON{
		Ticket: res.Ticket, Final: true, Slices: ids, Commit: res.SHA, HeadEnd: res.HeadEnd, Command: res.Command, Dir: res.Dir,
		Outcome: outcome, Exit: code, Seconds: res.Seconds, Covered: res.Covered, Uncovered: res.Uncovered,
		TreeBefore: res.TreeBefore, TreeAfter: res.TreeAfter, Recorded: recorded, NoRecord: req.NoRecord,
		Pushed: res.Pushed && res.PushErr == "", PushError: res.PushErr, Summary: res.Summary, Signature: res.Signature,
		pinnedJSON: pinnedJSONOf(res.Pinned),
	}
}
