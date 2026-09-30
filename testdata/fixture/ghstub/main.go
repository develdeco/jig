// Command ghstub is a fake `gh` binary used by the tests that need a github
// tracker without talking to GitHub: tracker's github-adapter tests,
// cmd/jig's ticket tests, and verifydeliver's publish tests (all build it
// through fixture.GhStub). It never talks to GitHub: it records every
// invocation's argv to $GH_STUB_LOG (one JSON array per line) and answers from
// a small canned set of responses keyed off the subcommand, tracking an
// incrementing issue counter in $GH_STUB_STATE so repeated "issue create"
// calls mint distinct numbers.
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
// edit" prints the URL it was given. Set $GH_STUB_FAIL to a subcommand pair,
// such as "pr create", to make that call fail the way gh does: a message on
// stderr and exit status 1.
package main

import (
	"encoding/json"
	"fmt"
	"os"
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
	case len(args) >= 2 && args[0] == "pr" && args[1] == "create":
		fmt.Println("https://github.example/owner/repo/pull/1")
	case len(args) >= 3 && args[0] == "pr" && args[1] == "edit":
		fmt.Println(args[2])
	default:
		fmt.Println("{}")
	}
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
