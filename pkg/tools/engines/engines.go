package engines

import (
	"context"

	"github.com/chainreactors/aiscan/pkg/tools/resources"
	"github.com/chainreactors/aiscan/pkg/telemetry"
	sdkfingers "github.com/chainreactors/sdk/fingers"
	"github.com/chainreactors/sdk/gogo"
	"github.com/chainreactors/sdk/neutron"
	"github.com/chainreactors/sdk/pkg/association"
	"github.com/chainreactors/sdk/spray"
	sdkzombie "github.com/chainreactors/sdk/zombie"
)

type Set struct {
	Fingers   *sdkfingers.Engine
	Gogo      *gogo.GogoEngine
	Spray     *spray.SprayEngine
	Neutron   *neutron.Engine
	Zombie    *sdkzombie.Engine
	Index     *association.FingerPOCIndex
	Resources *resources.Set
	Capacity  CapacityConfig
}

// CapacityConfig holds per-engine capacity limits. Zero means unlimited.
type CapacityConfig struct {
	Gogo    int // total concurrent scan threads (default: 5000)
	Spray   int // total concurrent HTTP threads (default: 200)
	Zombie  int // total concurrent auth threads (default: 500)
	Neutron int // total concurrent template executions (default: 10)
}

// DefaultCapacity returns sensible capacity defaults.
func DefaultCapacity() CapacityConfig {
	return CapacityConfig{
		Gogo:    800,
		Spray:   100,
		Zombie:  100,
		Neutron: 100,
	}
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

func Init(ctx context.Context, cyberhubURL, apiKey string) (*Set, error) {
	return InitWithLogger(ctx, cyberhubURL, apiKey, telemetry.NopLogger())
}

func InitWithLogger(ctx context.Context, cyberhubURL, apiKey string, logger telemetry.Logger) (*Set, error) {
	return InitWithOptions(ctx, resources.Options{
		CyberhubURL: cyberhubURL,
		APIKey:      apiKey,
		Mode:        resources.ModeMerge,
	}, logger)
}

func InitWithOptions(ctx context.Context, opts resources.Options, logger telemetry.Logger) (*Set, error) {
	return InitWithCapacity(ctx, opts, DefaultCapacity(), logger)
}

func InitWithCapacity(ctx context.Context, opts resources.Options, caps CapacityConfig, logger telemetry.Logger) (*Set, error) {
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
		logger.Infof("resources source=cyberhub mode=%s fingers=%d neutron=%d", resourceSet.Mode, resourceSet.RemoteFingers, resourceSet.RemoteNeutron)
		if resourceSet.RemoteFingersErr != nil {
			logger.Warnf("resources source=cyberhub type=fingers error=%q fallback=local", resourceSet.RemoteFingersErr)
		} else if resourceSet.RemoteFingers == 0 {
			logger.Warnf("resources source=cyberhub type=fingers count=0 fallback=local")
		}
		if resourceSet.RemoteNeutronErr != nil {
			logger.Warnf("resources source=cyberhub type=neutron error=%q fallback=local", resourceSet.RemoteNeutronErr)
		} else if resourceSet.RemoteNeutron == 0 {
			logger.Warnf("resources source=cyberhub type=neutron count=0 fallback=local")
		}
	}

	fEngine := resourceSet.Fingers
	if fEngine == nil {
		logger.Warnf("engine=fingers templates=0 action=disable")
	} else if fEngine.Count() > 0 {
		set.Fingers = fEngine
		logger.Infof("engine=fingers status=ready templates=%d", fEngine.Count())
	} else {
		logger.Warnf("engine=fingers templates=0 action=disable")
		_ = fEngine.Close()
	}

	nEngine := resourceSet.Neutron
	if nEngine != nil && nEngine.Count() > 0 {
		set.Neutron = nEngine
		logger.Infof("engine=neutron status=ready templates=%d", nEngine.Count())
	} else {
		logger.Warnf("engine=neutron templates=0 action=disable")
		if nEngine != nil {
			_ = nEngine.Close()
		}
	}

	if set.Neutron != nil {
		set.Index = association.NewFingerPOCIndex()
		set.Index.BuildFromTemplates(set.Neutron.Get())
		fingerCount, pocCount := set.Index.Count()
		logger.Infof("index=finger_poc status=ready fingers=%d pocs=%d", fingerCount, pocCount)
	}

	gogoConfig := gogo.NewConfig()
	gogoConfig.WithResourceProvider(resourceSet.GogoConfig)
	if set.Fingers != nil {
		gogoConfig.WithFingersEngine(set.Fingers)
	}
	if set.Neutron != nil {
		gogoConfig.WithNeutronEngine(set.Neutron)
	}
	if caps.Gogo > 0 {
		gogoConfig.WithCapacity(caps.Gogo)
	}
	set.Gogo = gogo.NewEngine(gogoConfig)
	logger.Infof("engine=gogo status=ready")

	sprayConfig := spray.NewConfig()
	sprayConfig.WithResourceProvider(resourceSet.SprayConfig)
	if set.Fingers != nil {
		sprayConfig.WithFingersEngine(set.Fingers)
	}
	if caps.Spray > 0 {
		sprayConfig.WithCapacity(caps.Spray)
	}
	set.Spray = spray.NewEngine(sprayConfig)
	logger.Infof("engine=spray status=ready")

	zombieConfig := sdkzombie.NewConfig()
	zombieConfig.WithResourceProvider(resourceSet.ZombieConfig)
	if caps.Zombie > 0 {
		zombieConfig.WithCapacity(caps.Zombie)
	}
	set.Zombie = sdkzombie.NewEngine(zombieConfig)
	if err := set.Zombie.Init(); err != nil {
		logger.Warnf("engine=zombie status=disabled error=%q", err)
		set.Zombie = nil
	} else {
		logger.Infof("engine=zombie status=ready")
	}

	if set.Neutron != nil && caps.Neutron > 0 {
		set.Neutron.SetCapacity(caps.Neutron)
	}

	set.Capacity = caps
	return set, nil
}
