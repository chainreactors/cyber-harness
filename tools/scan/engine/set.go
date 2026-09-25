package engine

import (
	"context"
	"fmt"
	"strings"

	"github.com/chainreactors/cyber/core/telemetry"
	"github.com/chainreactors/cyber/core/truncate"
	"github.com/chainreactors/cyber/tools/resources"
	"github.com/chainreactors/fingers/alias"
	fingersLib "github.com/chainreactors/fingers/fingers"
	sdkfingers "github.com/chainreactors/sdk/fingers"
	"github.com/chainreactors/sdk/gogo"
	"github.com/chainreactors/sdk/neutron"
	"github.com/chainreactors/sdk/pkg/association"
	"github.com/chainreactors/sdk/spray"
	sdkzombie "github.com/chainreactors/sdk/zombie"
)

// ReconOptions 提供 uncover 资产测绘引擎所需的凭证与默认行为。
type ReconOptions struct {
	FofaKey      string
	HunterAPIKey string
	Limit        int
	IngressProxy string // 给 uncover 的全局出站代理 (http://, https://, socks5://, socks5h://)
	Credentials  map[string]string
}

type Set struct {
	Fingers   *sdkfingers.Engine
	Gogo      *gogo.Engine
	Spray     *spray.Engine
	Neutron   *neutron.Engine
	Zombie    *sdkzombie.Engine
	Uncover   *UncoverEngine
	Index     *association.Index
	Resources *resources.Set
}

func (e *Set) Close() {
	if e.Fingers != nil {
		e.Fingers.Close()
	}
	if e.Gogo != nil {
		e.Gogo.Close()
	}
	if e.Spray != nil {
		e.Spray.Close()
	}
	if e.Neutron != nil {
		e.Neutron.Close()
	}
	if e.Zombie != nil {
		e.Zombie.Close()
	}
}

func InitWithOptions(ctx context.Context, opts resources.Options, logger telemetry.Logger) (*Set, error) {
	if logger == nil {
		logger = telemetry.NopLogger()
	}
	set := &Set{}

	resourceSet, err := resources.Init(ctx, opts)
	if err != nil {
		return nil, err
	}
	set.Resources = resourceSet
	if resourceSet.RemoteEnabled {
		logger.Infof("%s", telemetry.StartupOK("cyberhub", fmt.Sprintf("%s · %s fingers · %s neutron",
			resourceSet.Mode,
			truncate.FormatNumber(resourceSet.RemoteFingers),
			truncate.FormatNumber(resourceSet.RemoteNeutron))))
		if resourceSet.RemoteFingersErr != nil {
			logger.Warnf("%s", telemetry.StartupLine("skip", "cyberhub", fmt.Sprintf("fingers fallback=local reason=%q", resourceSet.RemoteFingersErr)))
		} else if resourceSet.RemoteFingers == 0 {
			logger.Warnf("%s", telemetry.StartupLine("skip", "cyberhub", "fingers fallback=local reason=\"empty\""))
		}
		if resourceSet.RemoteNeutronErr != nil {
			logger.Warnf("%s", telemetry.StartupLine("skip", "cyberhub", fmt.Sprintf("neutron fallback=local reason=%q", resourceSet.RemoteNeutronErr)))
		} else if resourceSet.RemoteNeutron == 0 {
			logger.Warnf("%s", telemetry.StartupLine("skip", "cyberhub", "neutron fallback=local reason=\"empty\""))
		}
	}

	fEngine := resourceSet.Fingers
	if fEngine == nil {
		logger.Warnf("%s", telemetry.StartupLine("skip", "fingers", "no templates"))
	} else if fEngine.Count() > 0 {
		set.Fingers = fEngine
		logger.Infof("%s", telemetry.StartupOK("fingers", truncate.FormatNumber(fEngine.Count())+" templates"))
	} else {
		logger.Warnf("%s", telemetry.StartupLine("skip", "fingers", "no templates"))
		_ = fEngine.Close()
	}

	nEngine := resourceSet.Neutron
	if nEngine != nil && nEngine.Count() > 0 {
		set.Neutron = nEngine
		logger.Infof("%s", telemetry.StartupOK("neutron", truncate.FormatNumber(nEngine.Count())+" templates"))
	} else {
		logger.Warnf("%s", telemetry.StartupLine("skip", "neutron", "no templates"))
		if nEngine != nil {
			_ = nEngine.Close()
		}
	}

	if set.Neutron != nil {
		set.Index = association.NewIndex()
		var fingers fingersLib.Fingers
		var aliases []*alias.Alias
		if set.Fingers != nil {
			fingers = set.Fingers.Fingers()
			aliases = set.Fingers.Aliases()
		}
		set.Index.BuildWithFingers(fingers, aliases, set.Neutron.Get())
		logger.Infof("%s", telemetry.StartupOK("finger-poc", fingerPOCDetail(len(fingers), len(aliases), len(set.Neutron.Get()))))
	}

	gogoConfig := gogo.NewConfig()
	gogoConfig.WithResourceProvider(resourceSet.GogoConfig)
	if set.Fingers != nil {
		gogoConfig.WithFingersEngine(set.Fingers)
	}
	if set.Neutron != nil {
		gogoConfig.WithNeutronEngine(set.Neutron)
	}
	if opts.Proxy != "" {
		gogoConfig.WithProxy(opts.Proxy)
	}
	gogoEngine, err := gogo.NewEngine(gogoConfig)
	if err != nil {
		logger.Warnf("%s", telemetry.StartupLine("fail", "gogo", err.Error()))
	} else {
		set.Gogo = gogoEngine
		logger.Infof("%s", telemetry.StartupOK("gogo", ""))
	}

	sprayConfig := spray.NewConfig()
	sprayConfig.WithResourceProvider(resourceSet.SprayConfig)
	if set.Fingers != nil {
		sprayConfig.WithFingersEngine(set.Fingers)
	}
	if opts.Proxy != "" {
		sprayConfig.WithProxy(opts.Proxy)
	}
	sprayEngine, err := spray.NewEngine(sprayConfig)
	if err != nil {
		logger.Warnf("%s", telemetry.StartupLine("fail", "spray", err.Error()))
	} else {
		set.Spray = sprayEngine
		logger.Infof("%s", telemetry.StartupOK("spray", ""))
	}

	zombieConfig := sdkzombie.NewConfig()
	zombieConfig.WithResourceProvider(resourceSet.ZombieConfig)
	if opts.Proxy != "" {
		zombieConfig.WithProxy(opts.Proxy)
	}
	zombieEngine, err := sdkzombie.NewEngine(zombieConfig)
	if err != nil {
		logger.Warnf("%s", telemetry.StartupLine("fail", "zombie", err.Error()))
	} else {
		set.Zombie = zombieEngine
		logger.Infof("%s", telemetry.StartupOK("zombie", ""))
	}

	return set, nil
}

func fingerPOCDetail(fingers, aliases, templates int) string {
	parts := []string{
		truncate.FormatNumber(fingers) + " fingers",
		truncate.FormatNumber(templates) + " templates",
	}
	if aliases > 0 {
		parts = append(parts, truncate.FormatNumber(aliases)+" aliases")
	}
	return strings.Join(parts, " · ")
}
