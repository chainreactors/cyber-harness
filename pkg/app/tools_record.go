//go:build full && record_ffmpeg && cgo && (windows || linux)

package app

import (
	"github.com/chainreactors/aiscan/core/capability"
	"github.com/chainreactors/aiscan/core/extension"
	"github.com/chainreactors/aiscan/pkg/exts/record"
)

func appendRecorderEntry(entries []extension.Entry, app *App, plan capability.Plan) ([]extension.Entry, error) {
	if !plan.Has("record") {
		return entries, nil
	}
	recorder, err := record.New(app.workDir)
	if err != nil {
		return nil, err
	}
	return append(entries, extension.Entry{ID: "record", Extension: recorder}), nil
}
