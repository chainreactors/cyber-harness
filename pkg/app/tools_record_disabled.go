//go:build full && (!record_ffmpeg || !cgo || (!windows && !linux))

package app

import (
	"github.com/chainreactors/aiscan/core/capability"
	"github.com/chainreactors/aiscan/core/extension"
)

func appendRecorderEntry(entries []extension.Entry, _ *App, _ capability.Plan) ([]extension.Entry, error) {
	return entries, nil
}
