package engine

import (
	"context"
	"net/http"
	"net/url"

	"github.com/chainreactors/aiscan/pkg/resources"
	"github.com/chainreactors/aiscan/pkg/telemetry"
	neutronhttp "github.com/chainreactors/neutron/protocols/http"
	"github.com/chainreactors/proxyclient"
	sdkfingers "github.com/chainreactors/sdk/fingers"
	"github.com/chainreactors/sdk/gogo"
	"github.com/chainreactors/sdk/neutron"
	"github.com/chainreactors/sdk/pkg/association"
	"github.com/chainreactors/sdk/spray"
	sdkzombie "github.com/chainreactors/sdk/zombie"
)

// ReconOptions 提供 uncover 资产测绘引擎所需的凭证与默认行为。
type ReconOptions struct {
	FofaEmail    string
	FofaKey      string
	HunterToken  string // 极少用 — 抓包出来的 web 登录 cookie/JWT, Python 原版 token 模式
	HunterAPIKey string // 华顺信安后台 API 管理生成的 api-key (推荐, 64 位 hex)
	Limit        int
	IngressProxy string // 给 uncover 的全局出站代理 (http://, https://, socks5://, socks5h://)
}

type Set struct {
	Fingers   *sdkfingers.Engine
	Gogo      *gogo.GogoEngine
	Spray     *spray.SprayEngine
	Neutron   *neutron.Engine
	Zombie    *sdkzombie.Engine
	Uncover   *UncoverEngine
	Index     *association.Index
	Resources *resources.Set
	Capacity  CapacityConfig
	Recon     ReconOptions
}

// CapacityConfig holds per-engine capacity limits. Zero means unlimited.
type CapacityConfig struct {
	Gogo    int // total concurrent scan threads (default: 5000)
	Spray   int // total concurrent HTTP threads (default: 200)
	Zombie  int // total concurrent auth threads (default: 500)
	Neutron int // total concurrent template executions (default: 10)
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
	if e.Uncover != nil {
		_ = e.Uncover.Close()
	}
}

func InitWithOptions(ctx context.Context, opts resources.Options, logger telemetry.Logger) (*Set, error) {
	return initWithCapacity(ctx, opts, CapacityConfig{}, opts.Proxy, logger)
}

func initWithCapacity(ctx context.Context, opts resources.Options, caps CapacityConfig, proxy string, logger telemetry.Logger) (*Set, error) {
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
		set.Index = association.NewIndex()
		set.Index.Build(nil, set.Neutron.Get())
		logger.Infof("index=finger_poc status=ready templates=%d", len(set.Neutron.Get()))
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
	if proxy != "" {
		gogoConfig.WithProxy(proxy)
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
	if proxy != "" {
		sprayConfig.WithProxy(proxy)
	}
	set.Spray = spray.NewEngine(sprayConfig)
	logger.Infof("engine=spray status=ready")

	zombieConfig := sdkzombie.NewConfig()
	zombieConfig.WithResourceProvider(resourceSet.ZombieConfig)
	if caps.Zombie > 0 {
		zombieConfig.WithCapacity(caps.Zombie)
	}
	if proxy != "" {
		zombieConfig.WithProxy(proxy)
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

	if proxy != "" {
		ApplyNeutronProxy(proxy)
	}

	set.Capacity = caps
	return set, nil
}

// ApplyNeutronProxy sets neutron DefaultOption/DefaultTransport proxy. The
// published neutron SDK does not yet support per-Config proxy, so we set the
// process-wide defaults. Each neutron execution creates its own transport clone,
// making this safe for concurrent use. Pass an empty string to clear.
func ApplyNeutronProxy(proxyURL string) {
	if proxyURL == "" {
		neutronhttp.DefaultOption.Proxy = nil
		neutronhttp.DefaultTransport.Proxy = nil
		neutronhttp.DefaultTransport.DialContext = nil
		return
	}
	u, err := url.Parse(proxyURL)
	if err != nil {
		return
	}
	dial, err := proxyclient.NewClient(u)
	if err != nil {
		return
	}
	neutronhttp.DefaultOption.Proxy = http.ProxyURL(u)
	neutronhttp.DefaultTransport.Proxy = http.ProxyURL(u)
	neutronhttp.DefaultTransport.DialContext = dial.DialContext
}
