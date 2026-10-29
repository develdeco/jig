package gitx

// treeEntryInProcess is treeEntryAtRev's lookup without the git program:
// the type of the entry at path in the tree of the commit rev names, and
// whether there is one. ok is false when the git program must answer, which
// includes a rev that does not resolve to a commit (git's message is the
// caller's error) and a form of rev or path the in-process reads do not take.
func treeEntryInProcess(dir, rev, path string, env envLookup) (entryType string, found, ok bool) {
	spec, parsed := parseRev(rev)
	if !parsed || spec.suffix != "" || !cleanTreePath(path) {
		return "", false, false
	}
	r := newReader(dir, env)
	defer r.close()
	spec.suffix = "^{commit}"
	commit, res := r.rev(spec)
	if res != resFound {
		return "", false, false
	}
	db, haveDB := r.objects()
	if !haveDB {
		return "", false, false
	}
	tree, st := db.peelTo(commit, "tree")
	if st != objFound {
		return "", false, false
	}
	switch e, st := db.entryAt(tree, path); st {
	case objFound:
		return e.entryType(), true, true
	case objNoEntry:
		return "", false, true
	}
	return "", false, false
}
