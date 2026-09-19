//go:build full && record && cgo && (windows || linux)

package main

// recordExtensionLinked reports whether this build contains the record
// extension. It is set by the same tags that gate the extension itself.
const recordExtensionLinked = true
