//go:build !(full && record && cgo && (windows || linux))

package main

const recordExtensionLinked = false
