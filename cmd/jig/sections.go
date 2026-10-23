package main

import (
	"fmt"
	"slices"
	"strings"

	"github.com/develdeco/jig/internal/locate"
	"github.com/develdeco/jig/internal/store"
)

// This file holds the extension points of two commands that many tickets add
// to: `jig here` (and the session-start context built from it) and `jig
// status`. A ticket that wants a block in either registers a section from an
// init() of its own file, so the command's file gains no hunk.

// sectionSet is a set of sections of one kind, unique by name and listed by
// order. Like the verb registry, it is filled by init() functions and only
// read after.
type sectionSet[T any] struct {
	kind  string
	name  func(T) string
	order func(T) int
	items []T
}

// add registers s, panicking on a nameless section or one whose name is
// taken: a registration is source code, so either is a bug found the first
// time the binary or its tests start.
func (r *sectionSet[T]) add(s T) {
	name := r.name(s)
	if name == "" {
		panic(fmt.Sprintf("register %s section: no Name", r.kind))
	}
	for _, have := range r.items {
		if r.name(have) == name {
			panic(fmt.Sprintf("register %s section: %q is already registered", r.kind, name))
		}
	}
	r.items = append(r.items, s)
}

// sorted returns the sections by Order, then by Name.
func (r *sectionSet[T]) sorted() []T {
	out := slices.Clone(r.items)
	slices.SortFunc(out, func(a, b T) int {
		if r.order(a) != r.order(b) {
			return r.order(a) - r.order(b)
		}
		return strings.Compare(r.name(a), r.name(b))
	})
	return out
}

// hereCtx is what a `jig here` section is given: the env, where the command
// found itself, the store it resolved (nil when none resolved), and the jig
// home root.
type hereCtx struct {
	e       env
	Where   locate.Where
	Store   *store.Store
	JigHome string
}

// hereSection is one block-producing part of `jig here`. Blocks returns the
// axi blocks it adds to the text output; JSON returns what it adds to
// `--json`. Either may be nil for a section with no form in that output, but
// not both.
type hereSection struct {
	Order  int
	Name   string
	Blocks func(hereCtx) ([]string, error)
	JSON   func(hereCtx) (any, error)
}

func newHereSections() *sectionSet[hereSection] {
	return &sectionSet[hereSection]{
		kind:  "here",
		name:  func(s hereSection) string { return s.Name },
		order: func(s hereSection) int { return s.Order },
	}
}

var defaultHereSections = newHereSections()

// registerHereSection adds s to `jig here`. It panics on a section with no
// name, a name already taken, or no Blocks and no JSON.
func registerHereSection(s hereSection) {
	if s.Blocks == nil && s.JSON == nil {
		panic(fmt.Sprintf("register here section: %q has neither Blocks nor JSON", s.Name))
	}
	defaultHereSections.add(s)
}

// hereSections returns the registered `jig here` sections by Order, then by
// Name.
func hereSections() []hereSection { return defaultHereSections.sorted() }

// statusSection is one block-producing part of `jig status`, for a ticket in
// a store. RenderStatus renders the registered blocks after its own and
// before its help line.
type statusSection struct {
	Order  int
	Name   string
	Blocks func(st *store.Store, ticket string) ([]string, error)
}

func newStatusSections() *sectionSet[statusSection] {
	return &sectionSet[statusSection]{
		kind:  "status",
		name:  func(s statusSection) string { return s.Name },
		order: func(s statusSection) int { return s.Order },
	}
}

var defaultStatusSections = newStatusSections()

// registerStatusSection adds s to `jig status`. It panics on a section with
// no name, a name already taken, or no Blocks.
func registerStatusSection(s statusSection) {
	if s.Blocks == nil {
		panic(fmt.Sprintf("register status section: %q has no Blocks", s.Name))
	}
	defaultStatusSections.add(s)
}

// statusSections returns the registered `jig status` sections by Order, then
// by Name.
func statusSections() []statusSection { return defaultStatusSections.sorted() }
