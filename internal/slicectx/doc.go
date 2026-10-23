// Package slicectx renders what a builder needs to start on a slice: the brief
// sections it builds from, the commits of the slices already verified and the
// files they touched, the code the graph links to its goal, how long the
// oracle took, the failed attempts so far, and the open questions. It backs
// `jig context`.
//
// The package holds no code yet; its directory and its place in
// ARCHITECTURE.md are reserved so the code that fills it lands in files of
// its own.
package slicectx
