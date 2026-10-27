// Package slicectx renders what a builder needs to start on a slice: the brief
// sections it builds from, the commits of the slices already verified and the
// files they touched, how long the oracle took, the failed attempts so far,
// and the questions and notes about it. It backs `jig context`, and is the
// pulled form of what slice.json carried when jig dispatched builders. It
// holds jig's own records and runs no context tool: the code is the builder's
// to find with its own tools (ADR 0034).
//
// Build reads the store, the journal and, when the slice builds in a clone,
// the clone itself (its manifest and its commits by Jig-Slice trailer);
// Context.Blocks renders the result in the jig text register, and Context
// marshals to JSON as it stands.
package slicectx
