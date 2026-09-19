//go:build !full

package scan

import "github.com/chainreactors/cyber/tools/scan/pipeline"

func extendKatanaProfile(string, *profile) {}

func (*Command) buildKatanaCapabilities(profile) []pipeline.Capability[event] {
	return nil
}
