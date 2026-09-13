//go:build full && record_ffmpeg && cgo && (windows || linux)

package aiscan

import (
	"github.com/chainreactors/aiscan/core/capability"
	"github.com/chainreactors/aiscan/core/extension"
	app "github.com/chainreactors/aiscan/pkg/app"
	"github.com/chainreactors/aiscan/pkg/exts/record"
	"github.com/chainreactors/aiscan/pkg/toolset"
)

func appendRecorderEntry(entries []extension.Entry, _ *app.App, tools *toolset.Registry, plan capability.Plan, workDir string) ([]extension.Entry, error) {
	if !plan.Has("record") {
		return entries, nil
	}
	recorder, err := record.New(tools, workDir)
	if err != nil {
		return nil, err
	}
	return append(entries, extension.Entry{ID: "record", Extension: recorder}), nil
}
