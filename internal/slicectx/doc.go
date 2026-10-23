// Package slicectx renders what a builder needs to start on a slice: the brief
// sections it builds from, the commits of the slices already verified and the
// files they touched, the code the graph links to its goal, how long the
// oracle took, the failed attempts so far, and the questions and notes about
// it. It backs `jig context`, and is the pulled form of what slice.json
// carried when jig dispatched builders.
//
// Build reads the store, the journal and, when the slice builds in a clone,
// the clone itself (its manifest, its commits by Jig-Slice trailer, and the
// code graph the project opted into); Context.Blocks renders the result in
// the jig text register, and Context marshals to JSON as it stands.
package slicectx
