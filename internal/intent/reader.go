package intent

import "time"

// Role identifies who produced a transcript message.
type Role string

const (
	RoleUser      Role = "user"
	RoleAssistant Role = "assistant"
)

// Message is one user or assistant turn extracted from a transcript. Tool
// calls and tool results are dropped from Text; FilePaths carries any file
// path a tool call named, exactly as the transcript recorded it (not yet
// relativized against the session's own cwd) - for matching only, never
// rendered into an excerpt.
type Message struct {
	Role      Role
	Text      string
	FilePaths []string
	Timestamp time.Time
	// FromSubagent is true for a message folded in from a subagent's own
	// transcript rather than read from the parent session's own file
	// (reader_claude.go's Discover). A RoleUser message with this set is
	// the parent agent's own prompt to that subagent, not the developer's
	// own words, even though it carries the same "user" role the
	// transcript format uses for both - RenderExcerpt labels it
	// accordingly, so the summarizer is never told to read it as the
	// developer's own ask.
	FromSubagent bool
}

// Session is one agent session discovered under a repo and time window,
// with its message bodies already loaded.
type Session struct {
	// Agent names the reader that discovered this session (Reader.Name),
	// recorded onto intent.md's own front matter.
	Agent string
	// ID is the session's own id, recorded onto intent.md's own front
	// matter. A subagent transcript is never returned as a Session of its
	// own - its messages are folded into its parent's - so this is always
	// a top-level session id, never a subagent's own "agent-<id>".
	ID string
	// CWD is the session's own recorded working directory, used both for
	// repo identity (Discover) and, by the matcher, as the fallback base
	// to relativize a tool-call path against when CWDTopLevel could not be
	// resolved.
	CWD string
	// CWDTopLevel is CWD's own git worktree top level (gitx.TopLevel),
	// resolved once at discovery time - empty when it could not be
	// resolved (a plain CWD that no longer exists, say). The matcher
	// relativizes a tool-call path against this, not CWD directly, when
	// it is set: CWD is often a subdirectory a session happened to start
	// in, and a diff file is always repo-root relative, so relativizing
	// against CWD instead would turn a path outside CWD but still inside
	// the repo into a bare, unmatchable basename instead of the nested
	// relative path a diff file itself uses.
	CWDTopLevel  string
	LastActivity time.Time
	Messages     []Message
}

// DiscoverOpts scopes one Discover call.
type DiscoverOpts struct {
	// Home is the user's home directory (production: os.UserHomeDir()); a
	// parameter so a reader never reaches a real one on its own.
	Home string
	// WindowStart and WindowEnd bound the inclusive file-mtime window a
	// session's own transcript file must fall in to be discovered at all.
	// The zero time on either side leaves that side unbounded.
	WindowStart, WindowEnd time.Time
	// RepoCommonDir is the git common dir (internal/gitx.CommonDir) a
	// session's own cwd must resolve to - the same directory, by
	// gitx.SameDir - for it to count as belonging to this repo. Empty means
	// nothing matches: Discover returns no sessions at all - there is no
	// repo to identify a session against.
	RepoCommonDir string
}

// Reader discovers agent sessions for one agent.
type Reader interface {
	// Name identifies the agent (e.g. "claude-code"), recorded onto
	// intent.md's own front matter.
	Name() string
	// Discover returns every session under opts's window and repo, with
	// message bodies already loaded - ready for the matcher, with nothing
	// further to fetch.
	Discover(opts DiscoverOpts) ([]*Session, error)
}
