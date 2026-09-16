//go:build full && (!record_ffmpeg || !cgo || (!windows && !linux))

package main

import (
	"github.com/chainreactors/cyber/core/capability"
	"github.com/chainreactors/cyber/core/extension"
	app "github.com/chainreactors/cyber/pkg/app"
	"github.com/chainreactors/cyber/pkg/toolset"
)

func appendRecorderEntry(entries []extension.Entry, _ app.Config, _ toolset.Runtime, _ capability.Plan, _ string) ([]extension.Entry, error) {
	return entries, nil
}
