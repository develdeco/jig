package main

import (
	"bufio"
	"fmt"
	"io"
	"path/filepath"
	"sort"
	"strings"

	"github.com/develdeco/jig/internal/axi"
	"github.com/develdeco/jig/internal/locate"
	"github.com/develdeco/jig/internal/project"
	"github.com/develdeco/jig/internal/repohost"
)

// init registers this file's verb with the registry (registry.go).
func init() {
	registerVerb(verb{
		Spec: cmdSpec{"share", "publish the project: set its remotes, push the store and the repo, and record the store in jig.yaml", []flagSpec{
			{"store-remote", "URL to make the store's origin", false},
			{"remote", "name=url: make url the origin of the repo called name (the project's own repo goes by the project's name); repeatable", false},
			{"create", "create each repository that has no remote through the host's CLI (gh), private", false},
			{"yes", "push without asking", false},
		}, false},
		Order: 404,
		Run: func(e env, args []string, stdout io.Writer, stdin io.Reader) int {
			return cmdShare(e, args, stdout, stdin)
		},
	})
}

// remoteFlag is a repeatable "--remote name=url" flag.Value.
type remoteFlag struct{ m map[string]string }

func (r *remoteFlag) String() string { return "" }

func (r *remoteFlag) Set(v string) error {
	name, url, ok := splitOnce(v, '=')
	if !ok || name == "" || url == "" {
		return fmt.Errorf("--remote must be name=url, got %q", v)
	}
	if r.m == nil {
		r.m = map[string]string{}
	}
	r.m[name] = url
	return nil
}

// absRemote makes a remote that is a path on this machine absolute against the
// working directory (slash-separated, so it reads the same in jig.yaml on
// every OS); a URL, and an empty remote, stay as they are.
func absRemote(e env, remote string) (string, error) {
	if remote == "" || project.IsRepoURL(remote) {
		return remote, nil
	}
	abs, err := e.abs(remote)
	if err != nil {
		return "", err
	}
	return filepath.ToSlash(abs), nil
}

// indented is lines with two leading spaces each, for a help block that lists
// something under a line of its own.
func indented(lines []string) []string {
	out := make([]string, len(lines))
	for i, l := range lines {
		out[i] = "  " + l
	}
	return out
}

// cmdShare implements `jig share [--store-remote <url>] [--remote <name>=<url>]...
// [--create] [--yes]` (decision 30 of the digester chart): a project is local
// until it is shared, and sharing is the push. It sets the remotes it is given
// (or creates the repositories through the host's CLI when asked), writes the
// store's remote into jig.yaml and commits it, pushes the store and the
// project's repo, and reports the nested repos that still have no remote,
// which collaborators cannot get.
func cmdShare(e env, args []string, stdout io.Writer, stdin io.Reader) int {
	fs := newFlagSet(e, "share")
	storeRemote := fs.String("store-remote", "", "URL to make the store's origin")
	var remotes remoteFlag
	fs.Var(&remotes, "remote", "name=url: make url the origin of the repo called name (the project's own repo goes by the project's name); repeatable")
	create := fs.Bool("create", false, "create each repository that has no remote through the host's CLI (gh), private")
	yes := fs.Bool("yes", false, "push without asking")
	positionals, handled, err := parseWithPositionals(stdout, fs, args)
	if handled {
		return 0
	} else if err != nil {
		return renderErr(stdout, err)
	}
	if len(positionals) > 0 {
		return renderErr(stdout, &axi.Error{Msg: fmt.Sprintf("jig share takes no argument, got %q", positionals[0]), Code: "VALIDATION_ERROR"})
	}

	p, err := findProject(e)
	if err != nil {
		return renderErr(stdout, err)
	}
	cwd, err := e.getwd()
	if err != nil {
		return renderErr(stdout, err)
	}
	where, err := locate.Locate(p.jigHome, cwd, locate.Opts{Getenv: e.getenv, StoreOnly: true})
	if err != nil {
		return renderErr(stdout, err)
	}
	if where.StorePath == "" {
		return renderErr(stdout, noStoreError(where))
	}

	// A remote given as a path is this directory's, not the repo's it is set
	// on: make it absolute before git reads it relative to some other.
	storeURL, err := absRemote(e, *storeRemote)
	if err != nil {
		return renderErr(stdout, err)
	}
	given := make(map[string]string, len(remotes.m))
	for name, url := range remotes.m {
		if given[name], err = absRemote(e, url); err != nil {
			return renderErr(stdout, err)
		}
	}
	plan, err := project.PlanShare(project.ShareOpts{
		Root:        p.root,
		Marker:      p.marker,
		StoreDir:    where.StorePath,
		StoreRemote: storeURL,
		Remotes:     given,
		Create:      *create,
	})
	if err != nil {
		return renderErr(stdout, err)
	}

	if !*yes {
		summary := plan.Summary()
		if !e.stdinIsTerminal(stdin) {
			return renderErr(stdout, &axi.Error{
				Msg:  "nothing pushed: sharing asks first, and there is no terminal to ask on",
				Code: axi.NeedsInput,
				Help: append(append([]string{"It would:"}, indented(summary)...), "Run `jig share` again with --yes (and the same flags) to do it"),
			})
		}
		fmt.Fprintf(stdout, "This will:\n  %s\nApply? [y/N] ", strings.Join(summary, "\n  "))
		answer, _ := bufio.NewReader(stdin).ReadString('\n')
		if a := strings.ToLower(strings.TrimSpace(answer)); a != "y" && a != "yes" {
			fmt.Fprintln(stdout)
			return renderErr(stdout, &axi.Error{Msg: "nothing pushed: sharing was declined", Code: axi.NeedsInput})
		}
	}

	res, err := project.RunShare(plan, func(name, dir string) error {
		gh, err := e.lookPath("gh")
		if err != nil {
			return &axi.Error{
				Msg:  "gh CLI is not installed: --create makes the repositories through it",
				Code: "GH_NOT_INSTALLED",
				Help: []string{"Install gh from https://cli.github.com, or create the repositories yourself and pass their URLs"},
			}
		}
		return repohost.CreateRepo(gh, nil, name, dir)
	})
	if err != nil {
		return renderErr(stdout, err)
	}

	committed := "no"
	if res.Committed {
		committed = "yes"
	}
	blocks := []string{axi.KV("share", [][2]string{
		{"project", p.marker.Project},
		{"store_remote", res.StoreRemote},
		{"marker_committed", committed},
	})}
	if len(res.Pushed) > 0 {
		rows := make([][]string, len(res.Pushed))
		for i, pr := range res.Pushed {
			name, branch, _ := strings.Cut(pr, " ")
			rows[i] = []string{name, branch}
		}
		blocks = append(blocks, axi.Table("pushed", []string{"repo", "branch"}, rows))
	}
	if len(res.Remotes) > 0 {
		names := make([]string, 0, len(res.Remotes))
		for n := range res.Remotes {
			names = append(names, n)
		}
		sort.Strings(names)
		rows := make([][]string, len(names))
		for i, n := range names {
			rows[i] = []string{n, res.Remotes[n]}
		}
		blocks = append(blocks, axi.Table("remotes_set", []string{"repo", "remote"}, rows))
	}
	var help []string
	if len(res.Without) > 0 {
		rows := make([][]string, len(res.Without))
		for i, w := range res.Without {
			rows[i] = []string{w}
		}
		blocks = append(blocks, axi.Table("without_remote", []string{"repo"}, rows))
		help = append(help, "A repo with no remote cannot be fetched by a collaborator: pass --remote <name>=<url> (or --create) for each")
	}
	help = append(help, "A collaborator clones the project's repo, then the store beside it: `jig here` names the command")
	blocks = append(blocks, axi.Help(help...))
	axi.Render(stdout, blocks...)
	return 0
}
