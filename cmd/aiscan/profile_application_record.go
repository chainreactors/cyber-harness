//go:build full && record_ffmpeg && cgo && (windows || linux)

package main

import (
	"github.com/chainreactors/aiscan/core/capability"
	"github.com/chainreactors/aiscan/core/extension"
	app "github.com/chainreactors/aiscan/pkg/app"
	"github.com/chainreactors/aiscan/pkg/exts/record"
	"github.com/chainreactors/aiscan/pkg/toolset"
	"path/filepath"
)

func appendRecorderEntry(entries []extension.Entry, config app.Config, tools toolset.Runtime, plan capability.Plan, workDir string) ([]extension.Entry, error) {
	if !plan.Has("record") {
		return entries, nil
	}
	options, err := record.ReadOptions(config.Resolved)
	if err != nil {
		return nil, err
	}
	recorder, err := record.New(tools, workDir, filepath.Join(config.DataDir, "record"), options.MaxConcurrent)
	if err != nil {
		return nil, err
	}
	return append(entries, extension.Entry{ID: "record", Extension: recorder}), nil
}
