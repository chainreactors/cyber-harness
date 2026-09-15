//go:build full && (!record_ffmpeg || !cgo || (!windows && !linux))

package edition

import "github.com/chainreactors/cyber/core/capability"

func recorderCapability() []capability.Descriptor { return nil }
