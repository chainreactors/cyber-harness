package engine

import (
	"context"
	"fmt"
	"net/http"
	"net/url"
	"strings"
	"sync"

	"github.com/chainreactors/aiscan/core/resources"
	"github.com/chainreactors/aiscan/core/telemetry"
	"github.com/chainreactors/aiscan/core/util"
	"github.com/chainreactors/fingers/alias"
	fingersLib "github.com/chainreactors/fingers/fingers"
	neutronhttp "github.com/chainreactors/neutron/protocols/http"
	"github.com/chainreactors/proxyclient"
	sdkfingers "github.com/chainreactors/sdk/fingers"
	"github.com/chainreactors/sdk/gogo"
	"github.com/chainreactors/sdk/neutron"
	"github.com/chainreactors/sdk/pkg/association"
	"github.com/chainreactors/sdk/spray"
	sdkzombie "github.com/chainreactors/sdk/zombie"
)

var neutronProxyMu sync.Mutex

// ReconOptions 提供 uncover 资产测绘引擎所需的凭证与默认行为。
type ReconOptions struct {
	FofaEmail    string
	FofaKey      string
	HunterToken  string // 极少用 — 抓包出来的 web 登录 cookie/JWT, Python 原版 token 模式
	HunterAPIKey string // 华顺信安后台 API 管理生成的 api-key (推荐, 64 位 hex)
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
		logger.Infof("%s", telemetry.StartupOK("cyberhub", fmt.Sprintf("%s · %s fingers · %s neutron",
			resourceSet.Mode,
			util.FormatNumber(resourceSet.RemoteFingers),
			util.FormatNumber(resourceSet.RemoteNeutron))))
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
		logger.Infof("%s", telemetry.StartupOK("fingers", util.FormatNumber(fEngine.Count())+" templates"))
	} else {
		logger.Warnf("%s", telemetry.StartupLine("skip", "fingers", "no templates"))
		_ = fEngine.Close()
	}

	nEngine := resourceSet.Neutron
	if nEngine != nil && nEngine.Count() > 0 {
		set.Neutron = nEngine
		logger.Infof("%s", telemetry.StartupOK("neutron", util.FormatNumber(nEngine.Count())+" templates"))
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
	if caps.Gogo > 0 {
		gogoConfig.WithCapacity(caps.Gogo)
	}
	if proxy != "" {
		gogoConfig.WithProxy(proxy)
	}
	var gogoEngine *gogo.Engine
	func() {
		restoreLogs := telemetry.SuppressGlobalNonErrors()
		defer restoreLogs()
		gogoEngine, err = gogo.NewEngine(gogoConfig)
	}()
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
	if caps.Spray > 0 {
		sprayConfig.WithCapacity(caps.Spray)
	}
	if proxy != "" {
		sprayConfig.WithProxy(proxy)
	}
	var sprayEngine *spray.Engine
	func() {
		restoreLogs := telemetry.SuppressGlobalNonErrors()
		defer restoreLogs()
		sprayEngine, err = spray.NewEngine(sprayConfig)
	}()
	if err != nil {
		logger.Warnf("%s", telemetry.StartupLine("fail", "spray", err.Error()))
	} else {
		set.Spray = sprayEngine
		logger.Infof("%s", telemetry.StartupOK("spray", ""))
	}

	zombieConfig := sdkzombie.NewConfig()
	zombieConfig.WithResourceProvider(resourceSet.ZombieConfig)
	if caps.Zombie > 0 {
		zombieConfig.WithCapacity(caps.Zombie)
	}
	if proxy != "" {
		zombieConfig.WithProxy(proxy)
	}
	zombieEngine, err := sdkzombie.NewEngine(zombieConfig)
	if err != nil {
		logger.Warnf("%s", telemetry.StartupLine("fail", "zombie", err.Error()))
	} else {
		set.Zombie = zombieEngine
		logger.Infof("%s", telemetry.StartupOK("zombie", ""))
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

func fingerPOCDetail(fingers, aliases, templates int) string {
	parts := []string{
		util.FormatNumber(fingers) + " fingers",
		util.FormatNumber(templates) + " templates",
	}
	if aliases > 0 {
		parts = append(parts, util.FormatNumber(aliases)+" aliases")
	}
	return strings.Join(parts, " · ")
}

// ApplyNeutronProxy sets neutron DefaultOption/DefaultTransport proxy. The
// published neutron SDK does not yet support per-Config proxy, so we set the
// process-wide defaults. Each neutron execution creates its own transport clone,
// making this safe for concurrent use. Pass an empty string to clear.
func ApplyNeutronProxy(proxyURL string) {
	neutronProxyMu.Lock()
	defer neutronProxyMu.Unlock()
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
