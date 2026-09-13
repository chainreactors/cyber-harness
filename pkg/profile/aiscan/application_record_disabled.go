//go:build full && (!record_ffmpeg || !cgo || (!windows && !linux))

package aiscan

import (
	"github.com/chainreactors/aiscan/core/capability"
	"github.com/chainreactors/aiscan/core/extension"
	app "github.com/chainreactors/aiscan/pkg/app"
	"github.com/chainreactors/aiscan/pkg/toolset"
)

func appendRecorderEntry(entries []extension.Entry, _ *app.App, _ *toolset.Registry, _ capability.Plan, _ string) ([]extension.Entry, error) {
	return entries, nil
}
