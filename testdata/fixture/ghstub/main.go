// Command ghstub is a fake `gh` binary used by the tests that need a GitHub
// pull-request host without talking to GitHub: repohost's own tests and
// verifydeliver's publish tests (both build it through fixture.GhStub). It
// never talks to GitHub: it records every
// invocation's argv and working directory to $GH_STUB_LOG (one JSON object
// per line, `{"argv": [...], "dir": "..."}`, argv[0] the stub binary itself)
// and answers from a small canned set of responses keyed off the subcommand,
// tracking an incrementing issue counter in $GH_STUB_STATE so repeated "issue
// create" calls mint distinct numbers.
//
// Pull requests: `api repos/<owner>/<repo>/pulls` lists $GH_STUB_PULLS, the
// JSON array of the pull requests the fake repo holds ("[]" when unset), the
// way the REST endpoint does: it takes the request as gh sends it (--method
// GET, since gh sends a POST, which would open a pull request, when
// parameters are given and no method is), and narrows the list by the state
// parameter (open when not given; open, closed or all), the head parameter
// (`<owner>:<branch>`, the owner compared without case) and the base
// parameter, then returns one page of it: per_page rows (30 when not given, at
// most 100) of page (the first when not given). A row is the endpoint's own:
// html_url, state ("open" when it says none), head.ref, head.user.login and
// base.ref; one that lacks head or base matches any. A list that is not a JSON
// array is printed as it is. "pr create" prints a pull request URL, and "pr
// edit" prints the URL it was given. "pr comment" succeeds silently. Set
// $GH_STUB_FAIL to a subcommand pair, such as "pr create", to make that call
// fail the way gh does: a message on stderr and exit status 1.
//
// `pr create --help` and `pr edit --help` (the adapter's own --attach support
// probe, one per subcommand) print help text naming --attach, unless
// $GH_STUB_NO_ATTACH is set, in which case it is left out of both
// subcommands' text - a gh too old to support --attach at all. gh 2.100.0 is
// the version whose own `pr create --help` documents --attach this way
// ("Pinning gh's own rewrite behavior against a real gh", DECISIONS.md).
//
// A `pr create` or `pr edit` carrying `--attach <file>` (one per file, in
// argv order, run with the evidence directory as this process's own working
// directory, so each is a bare name) models exactly what that same gh
// 2.100.0 was recorded doing against a real pull request, never a guess
// about what some gh does ("Pinning gh's own rewrite behavior against a real
// gh", DECISIONS.md): it rewrites a recognized markdown image reference to
// the file, `![alt](./<file>)` found anywhere in the body named by
// `--body-file`, replacing the path with a minted upload URL and leaving the
// alt text as it was; a file with no such reference in the body (a bare
// path, the one form that evidence showed left exactly as it stood) gets no
// in-place rewrite, but still gets uploaded, so gh appends a markdown link
// naming it, `[<file>](<url>)`, on a line of its own at the end of the body -
// the one record of that upload a caller can read back and place where its
// own reference stands. The resulting body is saved to $GH_STUB_BODY_STATE
// (when set) for a later `pr view` to answer with.
//
// `pr view` answers the body the adapter's ReadPRBody reads back: literally
// $GH_STUB_BODY when it is set (so a test can hand it any body, including one
// that defies what a real gh would do, to pin how a caller reacts to that);
// else $GH_STUB_BODY_STATE's own saved content, when an attach call wrote
// one; else a fixed canned body that always carries one unrewritten
// reference, "- ./demo-1.mp4: test video", when neither applies.
package main

import (
	"encoding/json"
	"fmt"
	"os"
	"regexp"
	"strconv"
	"strings"
)

func main() {
	logArgs(os.Getenv("GH_STUB_LOG"), os.Args)

	statePath := os.Getenv("GH_STUB_STATE")
	args := os.Args[1:]

	if fail := os.Getenv("GH_STUB_FAIL"); fail != "" && len(args) >= 2 && args[0]+" "+args[1] == fail {
		fmt.Fprintf(os.Stderr, "ghstub: %s failed on request\n", fail)
		os.Exit(1)
	}

	switch {
	case len(args) >= 2 && args[0] == "issue" && args[1] == "create":
		n := nextCounter(statePath)
		fmt.Printf("https://github.example/owner/repo/issues/%d\n", n)
	case len(args) >= 2 && args[0] == "api" && args[1] == "graphql":
		n := currentCounter(statePath)
		fmt.Printf(`{"data":{"repository":{"issue":{"id":"NODE%d"},"parent":{"id":"NODEP"}}}}`+"\n", n)
	case len(args) >= 2 && args[0] == "api" && strings.HasSuffix(args[1], "/pulls"):
		printPulls(args[2:], os.Getenv("GH_STUB_PULLS"))
	case len(args) >= 2 && args[0] == "pr" && args[1] == "create" && hasHelp(args):
		printAttachHelp("pr create")
	case len(args) >= 2 && args[0] == "pr" && args[1] == "edit" && hasHelp(args):
		printAttachHelp("pr edit")
	case len(args) >= 2 && args[0] == "pr" && args[1] == "create":
		processAttachments(args)
		fmt.Println("https://github.example/owner/repo/pull/1")
	case len(args) >= 3 && args[0] == "pr" && args[1] == "edit":
		processAttachments(args)
		fmt.Println(args[2])
	case len(args) >= 3 && args[0] == "pr" && args[1] == "comment":
		// pr comment succeeds silently
	case len(args) >= 2 && args[0] == "pr" && args[1] == "view":
		printPRBody()
	default:
		fmt.Println("{}")
	}
}

// hasHelp reports whether args asks for help, the way gh itself takes it:
// --help or -h, anywhere in the argument list.
func hasHelp(args []string) bool {
	for _, a := range args {
		if a == "--help" || a == "-h" {
			return true
		}
	}
	return false
}

// printAttachHelp prints subcommand's help text, naming --attach unless
// $GH_STUB_NO_ATTACH is set, the knob a test uses to model a gh too old to
// support --attach on either subcommand.
func printAttachHelp(subcommand string) {
	if os.Getenv("GH_STUB_NO_ATTACH") != "" {
		fmt.Printf("%s: usage help from a gh predating attachment support\n", subcommand)
		return
	}
	fmt.Println(`--attach FILE
  Attach files (e.g. screenshots, logs, media files) to the pull request.`)
}

// printPRBody answers `gh pr view ... --json body`: $GH_STUB_BODY's own text
// as the body when it is set, else $GH_STUB_BODY_STATE's own saved text when
// an attach call wrote one, else a fixed body that always carries one
// unrewritten media reference (see the package comment).
func printPRBody() {
	body := os.Getenv("GH_STUB_BODY")
	if body == "" {
		body = readBodyState(os.Getenv("GH_STUB_BODY_STATE"))
	}
	if body == "" {
		body = "## Intent\n\nA test intent\n\n## What changed\n\n- test change\n\n## Demo\n\n- ./demo-1.mp4: test video\n\n## Verification\n\ntest"
	}
	data, err := json.Marshal(map[string]string{"body": body})
	if err != nil {
		fmt.Fprintf(os.Stderr, "ghstub: %v\n", err)
		os.Exit(1)
	}
	fmt.Println(string(data))
}

// imageRefRE matches a markdown image reference to file, "./<file>" exactly,
// capturing its alt text so processAttachments can carry it over to the
// rewritten reference.
func imageRefRE(file string) *regexp.Regexp {
	return regexp.MustCompile(`!\[([^\]]*)\]\(\./` + regexp.QuoteMeta(file) + `\)`)
}

// attachedFiles returns the plain file names following each --attach flag
// in args, in argv order.
func attachedFiles(args []string) []string {
	var files []string
	for i, a := range args {
		if a == "--attach" && i+1 < len(args) {
			files = append(files, args[i+1])
		}
	}
	return files
}

// bodyFileArg returns the path following --body-file, or "".
func bodyFileArg(args []string) string {
	for i, a := range args {
		if a == "--body-file" && i+1 < len(args) {
			return args[i+1]
		}
	}
	return ""
}

// processAttachments simulates what `gh pr create`/`gh pr edit --attach`
// does to the body named by --body-file in args, for every file named by
// one of its own --attach flags (see the package comment for the exact
// rule), and saves the result to $GH_STUB_BODY_STATE for a later `pr view`
// to answer with. It does nothing when args carries no --attach flag or no
// --body-file.
func processAttachments(args []string) {
	files := attachedFiles(args)
	if len(files) == 0 {
		return
	}
	bodyPath := bodyFileArg(args)
	if bodyPath == "" {
		return
	}
	data, err := os.ReadFile(bodyPath)
	if err != nil {
		return
	}
	body := string(data)
	var appended []string
	for i, f := range files {
		url := fmt.Sprintf("https://github.example/user-attachments/assets/%d", i+1)
		re := imageRefRE(f)
		if re.MatchString(body) {
			body = re.ReplaceAllString(body, "![$1]("+url+")")
			continue
		}
		appended = append(appended, fmt.Sprintf("[%s](%s)", f, url))
	}
	if len(appended) > 0 {
		body = strings.TrimRight(body, "\n") + "\n\n" + strings.Join(appended, "\n") + "\n"
	}
	saveBodyState(os.Getenv("GH_STUB_BODY_STATE"), body)
}

// readBodyState returns path's own content, or "" when path is unset or
// cannot be read (no attach call has saved one yet).
func readBodyState(path string) string {
	if path == "" {
		return ""
	}
	data, err := os.ReadFile(path)
	if err != nil {
		return ""
	}
	return string(data)
}

// saveBodyState writes body to path, when path is set.
func saveBodyState(path, body string) {
	if path == "" {
		return
	}
	_ = os.WriteFile(path, []byte(body), 0o644)
}

// printPulls answers `gh api <repo>/pulls` with flags for the pull requests in
// list, a JSON array (see the package comment for how the flags narrow it).
func printPulls(flags []string, list string) {
	if list == "" {
		list = "[]"
	}
	var rows []map[string]json.RawMessage
	if err := json.Unmarshal([]byte(list), &rows); err != nil {
		fmt.Println(list)
		return
	}
	method := ""
	params := map[string]string{}
	for i := 0; i < len(flags); i++ {
		switch flags[i] {
		case "--method", "-X":
			if i+1 < len(flags) {
				method = flags[i+1]
				i++
			}
		case "-f", "-F", "--raw-field", "--field":
			if i+1 < len(flags) {
				key, value, _ := strings.Cut(flags[i+1], "=")
				params[key] = value
				i++
			}
		}
	}
	if method == "" && len(params) > 0 {
		method = "POST"
	}
	if method != "" && !strings.EqualFold(method, "GET") {
		fmt.Fprintf(os.Stderr, "ghstub: %s on the pull requests endpoint would open a pull request\n", method)
		os.Exit(1)
	}

	state := params["state"]
	if state == "" {
		state = "open"
	}
	matched := []map[string]json.RawMessage{}
	for _, row := range rows {
		if state != "all" && !strings.EqualFold(rowString(row, "state", "open"), state) {
			continue
		}
		if want := params["head"]; want != "" {
			owner, ref, _ := strings.Cut(want, ":")
			if !strings.EqualFold(rowString(row, "head.user.login", owner), owner) || rowString(row, "head.ref", ref) != ref {
				continue
			}
		}
		if want := params["base"]; want != "" && rowString(row, "base.ref", want) != want {
			continue
		}
		matched = append(matched, row)
	}

	perPage, page := 30, 1
	if n, err := strconv.Atoi(params["per_page"]); err == nil && n > 0 {
		perPage = min(n, 100)
	}
	if n, err := strconv.Atoi(params["page"]); err == nil && n > 0 {
		page = n
	}
	from := min((page-1)*perPage, len(matched))
	to := min(from+perPage, len(matched))
	data, err := json.Marshal(matched[from:to])
	if err != nil {
		fmt.Fprintf(os.Stderr, "ghstub: %v\n", err)
		os.Exit(1)
	}
	fmt.Println(string(data))
}

// rowString is the string a row holds under key (a dotted path reaches into
// the objects in it), or def when it has none.
func rowString(row map[string]json.RawMessage, key, def string) string {
	head, rest, nested := strings.Cut(key, ".")
	raw, ok := row[head]
	if !ok {
		return def
	}
	if nested {
		var obj map[string]json.RawMessage
		if err := json.Unmarshal(raw, &obj); err != nil {
			return def
		}
		return rowString(obj, rest, def)
	}
	var s string
	if err := json.Unmarshal(raw, &s); err != nil {
		return def
	}
	return s
}

// ghCall is one logged invocation: argv (argv[0] the stub binary itself) and
// the working directory it ran in, so a test that cares which directory gh
// was run in (`--attach` needs the evidence directory as cmd.Dir, so a file
// name alone resolves) can assert it.
type ghCall struct {
	Argv []string `json:"argv"`
	Dir  string   `json:"dir"`
}

func logArgs(path string, args []string) {
	if path == "" {
		return
	}
	dir, _ := os.Getwd()
	data, err := json.Marshal(ghCall{Argv: args, Dir: dir})
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

func readCounter(path string) int {
	if path == "" {
		return 0
	}
	data, err := os.ReadFile(path)
	if err != nil {
		return 0
	}
	n, err := strconv.Atoi(strings.TrimSpace(string(data)))
	if err != nil {
		return 0
	}
	return n
}

func writeCounter(path string, n int) {
	if path == "" {
		return
	}
	_ = os.WriteFile(path, []byte(strconv.Itoa(n)), 0o644)
}

// nextCounter increments and persists the issue counter, returning the new
// value (so the first minted issue is #1).
func nextCounter(path string) int {
	n := readCounter(path) + 1
	writeCounter(path, n)
	return n
}

// currentCounter reads the issue counter without incrementing it, so a
// GraphQL resolve call after a mint reports the just-minted number.
func currentCounter(path string) int {
	return readCounter(path)
}
