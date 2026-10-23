package store

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/develdeco/jig/internal/axi"
	"github.com/develdeco/jig/internal/gitx"
)

// The claims log is the one place the writers of a store agree on what no two
// of them may do at once: mint the same ticket id, or both drive one ticket.
// It is a single NDJSON file, claims.ndjson, whose lines are ClaimLines.
//
// In writers mode (Mode) the file lives alone on a branch of its own, claims,
// that no one checks out: a line is appended with git plumbing, a blob, a tree
// and a commit built on the tip of the branch, the ref moved with a compare
// and swap, and the branch pushed. A push the origin rejects (someone else
// appended first) is fetched, looked at again and tried once more. Whoever
// push the origin accepts first holds the id or the ticket. In single-main
// mode the file is claims.ndjson at the store root on main, and a line goes in
// through Claim, whose push and retry loop is the same race on main.
//
// The driver lives here, in the log every writer shares, and not in the
// machine ledger: the ledger is local to a machine, and the point of a driver
// is that the machine of another writer can see who it is.

const (
	// claimsBranch is the branch the claims log is on in writers mode.
	claimsBranch = "claims"
	// claimsFile is the name of the log: on the claims branch, and at the
	// store root of a single-main store.
	claimsFile = "claims.ndjson"

	claimsRef       = "refs/heads/" + claimsBranch
	claimsRemoteRef = "refs/remotes/origin/" + claimsBranch

	// claimPushRetries is how many times an append tries again after the
	// origin rejects its push, so two writers racing can never chase each
	// other forever.
	claimPushRetries = 5
)

// The kinds of claim line.
const (
	// ClaimID reserves a ticket id (or an alias): no other writer mints it.
	ClaimID = "id"
	// ClaimDriver names the writer driving a ticket.
	ClaimDriver = "driver"
	// ClaimRelease ends the driver line of the same writer for the ticket.
	ClaimRelease = "release"
)

// ClaimLine is one line of the claims log. Wire keys are exact; every key but
// ts, kind and ticket is omitted when empty.
type ClaimLine struct {
	// TS is when the line was written: UTC, RFC 3339 with nanoseconds.
	TS string `json:"ts"`
	// Kind is ClaimID, ClaimDriver or ClaimRelease.
	Kind string `json:"kind"`
	// Ticket is the id the line is about: the id reserved, or the ticket
	// driven or released.
	Ticket string `json:"ticket"`
	// Slice is the slice the claim was made for, when it was made for one.
	Slice string `json:"slice,omitempty"`
	// Writer is the writer name (git config jig.writer, else the host name) of
	// the clone that wrote the line. The lines of the root commit a migration
	// writes name none.
	Writer string `json:"writer,omitempty"`
	// Session is the harness session the claim was made from.
	Session string `json:"session,omitempty"`
	// Author is the name of the person whose git identity wrote the line.
	Author string `json:"author,omitempty"`
}

// errIDTaken is what a decision returns for an id the log already holds.
var errIDTaken = errors.New("store: the id is already claimed")

// errNoClaim is the sentinel the write of fileClaimsTx returns when its
// decision found there is nothing left to append: Claim hands it back and the
// caller reports that nothing was appended, not an error.
var errNoClaim = errors.New("store: nothing to claim")

// claimDecision looks at the log as it stands and says what to append: a
// line, nothing (nil: the log already says what the caller wanted), or an
// error that refuses. An append runs it again each time it goes round after a
// rejected push, on the log as the origin now has it, so the decision a
// racing writer lost is made over.
type claimDecision func(lines []ClaimLine) (*ClaimLine, error)

// Claims returns the claims log, in the order it was written. A store with no
// log reads as no lines; a line that is not a JSON line is left out.
func (s *Store) Claims() ([]ClaimLine, error) {
	writers, err := s.Mode()
	if err != nil {
		return nil, err
	}
	if writers {
		view, err := s.readClaimsBranch()
		if err != nil {
			return nil, err
		}
		return parseClaims(view.data), nil
	}
	data, err := os.ReadFile(filepath.Join(s.Root, claimsFile))
	if errors.Is(err, os.ErrNotExist) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	return parseClaims(string(data)), nil
}

// parseClaims decodes the lines of a claims file, leaving out blank and
// malformed ones.
func parseClaims(data string) []ClaimLine {
	var out []ClaimLine
	for _, raw := range strings.Split(data, "\n") {
		raw = strings.TrimSpace(raw)
		if raw == "" || raw[0] != '{' {
			continue
		}
		var l ClaimLine
		if err := json.Unmarshal([]byte(raw), &l); err != nil {
			continue
		}
		out = append(out, l)
	}
	return out
}

// AppendClaim appends l to the claims log: the writer, the author and the
// time are filled in when l has none. It decides nothing - a caller that must
// not append what is already there (a driver, an id) uses ClaimDriver or Mint.
func (s *Store) AppendClaim(l ClaimLine) error {
	_, err := s.claimsTx(func([]ClaimLine) (*ClaimLine, error) { return &l, nil })
	return err
}

// Driver returns the line that makes a writer the driver of ticket: the
// latest driver line for it that no release line from the same writer has
// ended since. ok is false when no one drives it.
func (s *Store) Driver(ticket string) (line ClaimLine, ok bool, err error) {
	lines, err := s.Claims()
	if err != nil {
		return ClaimLine{}, false, err
	}
	cur, held := driverOf(lines, ticket)
	return cur, held, nil
}

// driverOf folds lines into the driver of ticket.
func driverOf(lines []ClaimLine, ticket string) (ClaimLine, bool) {
	var cur ClaimLine
	held := false
	for _, l := range lines {
		if l.Ticket != ticket {
			continue
		}
		switch l.Kind {
		case ClaimDriver:
			cur, held = l, true
		case ClaimRelease:
			if held && cur.Writer == l.Writer {
				cur, held = ClaimLine{}, false
			}
		}
	}
	return cur, held
}

// DriverHeldCode is the refusal for a ticket another writer drives.
const DriverHeldCode = "DRIVER_HELD"

// ClaimDriver makes the writer of this clone the driver of ticket, from
// session. One writer drives a ticket at a time: when another writer has a
// driver line that was not released, it is refused with DRIVER_HELD unless
// take is set, which takes the ticket over by writing a driver line of its
// own (the later line wins). A writer that already drives the ticket writes
// nothing, whatever the session: which of the sessions of one writer holds
// which slice is a matter for the slices. The line goes to the claims log,
// where every writer sees it once it is pushed (see the top of this file);
// the check and the append are one step on that log, so two writers claiming
// at once cannot both win.
func (s *Store) ClaimDriver(ticket, session string, take bool) error {
	me, err := s.writerName()
	if err != nil {
		return err
	}
	_, err = s.claimsTx(func(lines []ClaimLine) (*ClaimLine, error) {
		cur, held := driverOf(lines, ticket)
		if held && cur.Writer == me {
			return nil, nil
		}
		if held && !take {
			return nil, driverHeldError(ticket, cur)
		}
		return &ClaimLine{Kind: ClaimDriver, Ticket: ticket, Writer: me, Session: session}, nil
	})
	return err
}

// ReleaseDriver ends the drive of ticket by the writer of this clone, when
// it has one; it writes nothing for a ticket the writer does not drive.
func (s *Store) ReleaseDriver(ticket, session string) error {
	me, err := s.writerName()
	if err != nil {
		return err
	}
	_, err = s.claimsTx(func(lines []ClaimLine) (*ClaimLine, error) {
		if cur, held := driverOf(lines, ticket); !held || cur.Writer != me {
			return nil, nil
		}
		return &ClaimLine{Kind: ClaimRelease, Ticket: ticket, Writer: me, Session: session}, nil
	})
	return err
}

// driverHeldError is the DRIVER_HELD refusal for a ticket that cur drives.
func driverHeldError(ticket string, cur ClaimLine) error {
	who := cur.Writer
	if who == "" {
		who = "another writer"
	}
	if cur.Author != "" {
		who += " (" + cur.Author + ")"
	}
	if cur.Session != "" {
		who += ", session " + cur.Session
	}
	return &axi.Error{
		Msg:  fmt.Sprintf("ticket %s is driven by %s", ticket, who),
		Code: DriverHeldCode,
		Help: []string{fmt.Sprintf("If they are not working on it any more, take it over: `jig claim %s --take`", ticket)},
	}
}

// reserveID writes an id line for id to the claims log, unless the log has
// one already. taken says the log did: someone else minted id first (or this
// clone did earlier and the ticket never landed), so the caller picks the next
// one. The log it was refused on is the one of the origin, as the rejected
// push fetched it, so the next id the caller computes (nextID) skips id.
func (s *Store) reserveID(id string) (taken bool, err error) {
	me, err := s.writerName()
	if err != nil {
		return false, err
	}
	_, err = s.claimsTx(func(lines []ClaimLine) (*ClaimLine, error) {
		for _, l := range lines {
			if l.Kind == ClaimID && l.Ticket == id {
				return nil, errIDTaken
			}
		}
		return &ClaimLine{Kind: ClaimID, Ticket: id, Writer: me}, nil
	})
	if errors.Is(err, errIDTaken) {
		return true, nil
	}
	return false, err
}

// claimedIDNumber returns the highest number the claims log holds an id line
// for under key, 0 when it holds none: the next number nextID gives is one
// past it.
func (s *Store) claimedIDNumber(key string) (int, error) {
	lines, err := s.Claims()
	if err != nil {
		return 0, err
	}
	maxN := 0
	for _, l := range lines {
		if l.Kind != ClaimID {
			continue
		}
		m := ticketIDPattern.FindStringSubmatch(l.Ticket)
		if m == nil || m[1] != key {
			continue
		}
		if n, err := strconv.Atoi(m[2]); err == nil && n > maxN {
			maxN = n
		}
	}
	return maxN, nil
}

// claimMessage is the commit message of the commit that appends l.
func claimMessage(l ClaimLine) string {
	return fmt.Sprintf("jig: %s %s by %s", l.Kind, l.Ticket, l.Writer)
}

// completeClaim fills in what an appended line leaves to the store: the time
// and the author, and the writer when the caller named none.
func completeClaim(l *ClaimLine, writer string, id gitx.Identity) {
	if l.TS == "" {
		l.TS = time.Now().UTC().Format(time.RFC3339Nano)
	}
	if l.Writer == "" {
		l.Writer = writer
	}
	if l.Author == "" {
		l.Author = id.Name
	}
}

// encodeClaim is l as one log line, newline included.
func encodeClaim(l ClaimLine) (string, error) {
	b, err := json.Marshal(l)
	if err != nil {
		return "", err
	}
	return string(b) + "\n", nil
}

// claimsTx runs decide over the claims log and appends the line it asks for,
// atomically with respect to every other writer: in writers mode as a
// compare-and-swap on the claims branch, in single-main mode through Claim.
// It reports whether anything was appended; a decision that returns no line
// appends nothing and costs no write, no identity lookup and no push.
func (s *Store) claimsTx(decide claimDecision) (appended bool, err error) {
	writers, err := s.Mode()
	if err != nil {
		return false, err
	}
	if writers {
		return s.branchClaimsTx(decide)
	}
	return s.fileClaimsTx(decide)
}

// fileClaimsTx is claimsTx for a single-main store: the log is claims.ndjson
// at the store root, and the append is a Claim, so it is committed alone and
// pushed with the retry of Claim (a push the origin rejects is undone, pulled
// and decided again).
func (s *Store) fileClaimsTx(decide claimDecision) (bool, error) {
	// A decision that needs no write is made without the lock of Claim and
	// without asking git who the person is.
	lines, err := s.Claims()
	if err != nil {
		return false, err
	}
	line, err := decide(lines)
	if err != nil || line == nil {
		return false, err
	}
	me, err := s.writerName()
	if err != nil {
		return false, err
	}
	path := filepath.Join(s.Root, claimsFile)
	_, err = s.Claim(
		func() (string, []string, error) {
			lines, err := s.Claims()
			if err != nil {
				return "", nil, err
			}
			line, err := decide(lines)
			if err != nil {
				return "", nil, err
			}
			if line == nil {
				return "", nil, errNoClaim
			}
			id, err := s.identity()
			if err != nil {
				return "", nil, err
			}
			completeClaim(line, me, id)
			enc, err := encodeClaim(*line)
			if err != nil {
				return "", nil, err
			}
			data, err := os.ReadFile(path)
			if err != nil && !errors.Is(err, os.ErrNotExist) {
				return "", nil, err
			}
			if len(data) > 0 && data[len(data)-1] != '\n' {
				data = append(data, '\n')
			}
			if err := AtomicWrite(path, append(data, enc...)); err != nil {
				return "", nil, err
			}
			return line.Ticket, []string{claimsFile}, nil
		},
		func(string) string { return claimMessage(*line) },
	)
	if errors.Is(err, errNoClaim) {
		return false, nil
	}
	return err == nil, err
}

// claimsView is the claims branch as one read of it found it.
type claimsView struct {
	// tip is the commit the view was read at, "" when there is no claims
	// branch yet.
	tip string
	// local says tip was read from refs/heads/claims, rather than from the
	// branch of that name on the origin (a clone made after the store moved to
	// writers mode has only that).
	local bool
	// data is claims.ndjson at tip, without its final newline.
	data string
}

// readClaimsBranch reads the claims log from the claims branch of the clone,
// or failing that the one of the origin as last fetched.
func (s *Store) readClaimsBranch() (claimsView, error) {
	for _, ref := range []string{claimsRef, claimsRemoteRef} {
		ok, err := gitx.RefExists(s.Root, ref)
		if err != nil {
			return claimsView{}, err
		}
		if !ok {
			continue
		}
		tip, err := gitx.RevParse(s.Root, ref)
		if err != nil {
			return claimsView{}, err
		}
		data, err := s.claimsBlob(tip)
		if err != nil {
			return claimsView{}, err
		}
		return claimsView{tip: tip, local: ref == claimsRef, data: data}, nil
	}
	return claimsView{}, nil
}

// claimsBlob returns claims.ndjson as it is in commit, "" when the commit has
// no such file.
func (s *Store) claimsBlob(commit string) (string, error) {
	spec := commit + ":" + claimsFile
	data, err := gitx.Run(s.Root, "cat-file", "blob", spec)
	if err == nil {
		return data, nil
	}
	if _, verr := gitx.Run(s.Root, "rev-parse", "--verify", "--quiet", spec); verr != nil {
		return "", nil // the commit is there and the file is not: an empty log
	}
	return "", err
}

// branchClaimsTx is claimsTx for a store in writers mode: the append is
// plumbing on the claims branch.
func (s *Store) branchClaimsTx(decide claimDecision) (bool, error) {
	release, err := s.LockCheckpoint()
	if err != nil {
		return false, err
	}
	defer release()
	me, err := s.writerName()
	if err != nil {
		return false, err
	}
	remote := s.hasOrigin()

	var ident gitx.Identity
	haveIdent, refreshed := false, false
	for attempt := 0; ; attempt++ {
		view, err := s.readClaimsBranch()
		if err != nil {
			return false, err
		}
		line, err := decide(parseClaims(view.data))
		if err != nil && remote && !refreshed {
			// A refusal is only as good as the log it was made on, which may
			// be as old as the last fetch: look at the log of the origin
			// before refusing anyone. If it cannot be reached, the refusal
			// stands.
			refreshed = true
			if s.fetchClaims() == nil {
				continue
			}
		}
		if err != nil || line == nil {
			return false, err
		}
		if !haveIdent {
			if ident, err = s.identity(); err != nil {
				return false, err
			}
			haveIdent = true
		}
		completeClaim(line, me, ident)
		enc, err := encodeClaim(*line)
		if err != nil {
			return false, err
		}
		data := view.data
		if data != "" {
			data += "\n"
		}
		commit, err := s.commitClaims(ident, view.tip, data+enc, claimMessage(*line))
		if err != nil {
			return false, err
		}
		old := strings.Repeat("0", len(commit))
		if view.local {
			old = view.tip
		}
		if _, err := gitx.Run(s.Root, "update-ref", claimsRef, commit, old); err != nil {
			return false, err
		}
		if !remote {
			return true, nil
		}
		out, perr := gitx.RunRaw(s.Root, "push", "origin", claimsRef+":"+claimsRef)
		if perr == nil {
			return true, nil
		}
		// Not pushed, so not claimed: put the branch back as it was.
		if view.local {
			_, _ = gitx.Run(s.Root, "update-ref", claimsRef, view.tip, commit)
		} else {
			_, _ = gitx.Run(s.Root, "update-ref", "-d", claimsRef, commit)
		}
		if !claimsPushRaced(out) {
			return false, claimsNotPushedError(perr)
		}
		if attempt >= claimPushRetries {
			return false, claimsNotPushedError(fmt.Errorf("the origin rejected every push after %d tries", attempt+1))
		}
		// Someone else appended first: take their log and decide again.
		if err := s.fetchClaims(); err != nil {
			return false, claimsNotPushedError(err)
		}
		time.Sleep(time.Duration(attempt+1) * 20 * time.Millisecond)
	}
}

// claimsPushRaced reports whether out, the output of a failed push of the
// claims branch, says another writer got there first: the usual rejection (the
// branch moved on, not a fast-forward) or, when two pushes reach the origin in
// the same instant, the loser of the ref lock (the origin refuses to move a
// ref that is no longer where the push expected it). Either is worth fetching
// and trying again; anything else is not.
func claimsPushRaced(out string) bool {
	return pushRejectedText(out) ||
		strings.Contains(out, "incorrect old value") ||
		strings.Contains(out, "cannot lock ref") ||
		strings.Contains(out, "[remote rejected]")
}

// commitClaims stores content as claims.ndjson in a new commit on top of
// parent (a root commit when parent is empty), authored as ident, and returns
// it. Nothing refers to the commit yet.
func (s *Store) commitClaims(ident gitx.Identity, parent, content, msg string) (string, error) {
	blob, err := gitx.RunInput(s.Root, []byte(content), "hash-object", "-w", "--stdin")
	if err != nil {
		return "", err
	}
	tree, err := gitx.RunInput(s.Root, []byte("100644 blob "+blob+"\t"+claimsFile+"\n"), "mktree")
	if err != nil {
		return "", err
	}
	args := []string{"commit-tree", tree, "-m", msg}
	if parent != "" {
		args = append(args, "-p", parent)
	}
	return gitx.Run(s.Root, commitConfig(ident, args...)...)
}

// fetchClaims brings the claims branch of the origin in and points the claims
// branch of the clone at it. The local branch is only ever a copy of the one
// of the origin plus, for the moment between update-ref and push, one line not
// yet acknowledged; a copy that has gone past the origin is that
// unacknowledged line of a process that died there, and is dropped.
func (s *Store) fetchClaims() error {
	if _, err := gitx.Run(s.Root, "fetch", "--quiet", "origin", "+"+claimsRef+":"+claimsRemoteRef); err != nil {
		return err
	}
	return s.followClaims()
}

// followClaims points refs/heads/claims at refs/remotes/origin/claims, when
// the origin has one and the clone differs.
func (s *Store) followClaims() error {
	ok, err := gitx.RefExists(s.Root, claimsRemoteRef)
	if err != nil || !ok {
		return err
	}
	remote, err := gitx.RevParse(s.Root, claimsRemoteRef)
	if err != nil {
		return err
	}
	have, err := gitx.RefExists(s.Root, claimsRef)
	if err != nil {
		return err
	}
	if have {
		if local, err := gitx.RevParse(s.Root, claimsRef); err != nil {
			return err
		} else if local == remote {
			return nil
		}
	}
	_, err = gitx.Run(s.Root, "update-ref", claimsRef, remote)
	return err
}

// claimsNotPushedCode is the refusal for a claim the origin did not take.
const claimsNotPushedCode = "CLAIMS_NOT_PUSHED"

// claimsNotPushedError wraps cause as the refusal for an append that did not
// reach the origin: it is not a claim until it has.
func claimsNotPushedError(cause error) error {
	return &axi.Error{
		Msg:  fmt.Sprintf("the claim could not be pushed to the origin of the store: %v", cause),
		Code: claimsNotPushedCode,
		Help: []string{"Retry once the origin is reachable"},
	}
}

// hasOrigin reports whether the clone has a remote named origin, in process
// when the repository allows it.
func (s *Store) hasOrigin() bool {
	if repo, err := gitx.OpenRepo(s.Root); err == nil {
		return repo.HasRemote("origin")
	}
	return s.HasRemote()
}
