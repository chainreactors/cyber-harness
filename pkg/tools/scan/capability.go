package scan

import "context"

const (
	capGogoPortscan   = "gogo_portscan"
	capSprayCheck     = "spray_check"
	capSprayFinger    = "spray_finger"
	capCoreWeb        = "core_web"
	capSprayCommon    = "spray_common"
	capSprayBackup    = "spray_backup"
	capSprayActive    = "spray_active"
	capSprayCrawl     = "spray_crawl"
	capSprayBrute     = "spray_brute"
	capZombieWeakpass = "zombie_weakpass"
	capNeutronPOC     = "neutron_poc"
	capAgentVerify    = "agent_verify"
)

type emitFunc func(event)

type capability struct {
	Name   string
	Accept func(event) bool
	Worker int
	RunKey func(event) string
	Run    func(context.Context, event, emitFunc)
}

func (c capability) keyFor(e event) string {
	if c.RunKey != nil {
		return c.RunKey(e)
	}
	return c.Name + "|" + e.key()
}

func acceptsTarget(kinds ...targetKind) func(event) bool {
	set := targetInputs(kinds...)
	return func(e event) bool {
		if e.Kind != eventTarget || e.Target == nil {
			return false
		}
		_, ok := set[e.Target.Kind()]
		return ok
	}
}

func targetInputs(kinds ...targetKind) map[targetKind]struct{} {
	out := make(map[targetKind]struct{}, len(kinds))
	for _, kind := range kinds {
		out[kind] = struct{}{}
	}
	return out
}
