// Package exts is the adapter layer between feature implementations and the
// harness lifecycle. Every feature subtree here declares at least one
// Extension, so mounting a feature is adding it to a composition root rather
// than teaching the host about it.
package exts
