package scanner

import (
	coretool "github.com/chainreactors/cyber/core/tool"
	"github.com/chainreactors/cyber/tools/scan/engine"
)

// Availability reports the loaded command declarations and their owned
// resources. Names alone is a build catalog, not a readiness assertion.
type CommandState struct {
	Name   string `json:"name"`
	State  string `json:"state"`
	Reason string `json:"reason,omitempty"`
}
type Availability struct {
	Commands []CommandState `json:"commands"`
}

func availability(commands []coretool.Command, engines *engine.Set) *Availability {
	registered := map[string]bool{}
	for _, command := range commands {
		registered[command.Name] = true
	}
	out := &Availability{}
	for _, name := range append(Names(), "cyberhub") {
		state := CommandState{Name: name, State: "ready"}
		if !registered[name] {
			state.State = "unavailable"
			state.Reason = "required scanner engine is unavailable"
		} else if name != "curl" && name != "katana" {
			if engines == nil || engines.Resources == nil {
				state.State = "degraded"
				state.Reason = "scanner resources are unavailable"
			} else if engines.Resources.RemoteEnabled && (engines.Resources.RemoteFingersErr != nil || engines.Resources.RemoteNeutronErr != nil) {
				state.State = "degraded"
				state.Reason = "configured CyberHub resources failed; using available local resources"
			}
			if name == "cyberhub" && (engines == nil || engines.Index == nil) {
				state.State = "unavailable"
				state.Reason = "fingerprint and POC index is unavailable"
			}
		}
		out.Commands = append(out.Commands, state)
	}
	return out
}
