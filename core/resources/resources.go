package resources

//go:generate go run ./templates_gen.go -t ../../templates -o template.go -embed

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"time"

	fingerslib "github.com/chainreactors/fingers/fingers"
	fingerresources "github.com/chainreactors/fingers/resources"
	"github.com/chainreactors/neutron/templates"
	"github.com/chainreactors/sdk/fingers"
	"github.com/chainreactors/sdk/neutron"
	"github.com/chainreactors/sdk/pkg/cyberhub"
	"github.com/chainreactors/utils"
	"gopkg.in/yaml.v3"
)

const (
	ModeMerge    = "merge"
	ModeOverride = "override"
)

// Options controls aiscan-owned scanner resource loading.
type Options struct {
	CyberhubURL string
	APIKey      string
	Mode        string
	Proxy       string
}

// Set owns the scanner resource bytes and compiled SDK engines used by aiscan.
type Set struct {
	Mode             string
	RemoteEnabled    bool
	RemoteFingers    int
	RemoteNeutron    int
	RemoteFingersErr error
	RemoteNeutronErr error
	FingersConfig    *fingers.Config
	NeutronConfig    *neutron.Config
	Fingers          *fingers.Engine
	Neutron          *neutron.Engine
	gogoConfigs      map[string][]byte
	sprayConfigs     map[string][]byte
	zombieConfigs    map[string][]byte
}

// Init loads scanner resources once for aiscan and prepares SDK configs.
func Init(ctx context.Context, opts Options) (*Set, error) {
	mode, err := NormalizeMode(opts.Mode)
	if err != nil {
		return nil, err
	}

	localHTTP, localSocket, err := loadLocalFingers()
	if err != nil {
		return nil, err
	}
	if err := installLocalPortPreset(); err != nil {
		return nil, err
	}
	localFingers := append(append(fingerslib.Fingers(nil), localHTTP...), localSocket...)
	localFullFingers := (fingers.FullFingers{}).Merge(localFingers, nil)
	finalFullFingers := cloneFullFingers(localFullFingers)
	finalTemplates := loadLocalTemplates()

	set := &Set{
		Mode:          mode,
		RemoteEnabled: opts.CyberhubURL != "" && opts.APIKey != "",
		gogoConfigs:   defaultGogoConfigs(),
		sprayConfigs:  defaultSprayConfigs(),
		zombieConfigs: defaultZombieConfigs(),
	}

	if set.RemoteEnabled {
		fingerCache := cachePath(opts.CyberhubURL, opts.APIKey, "fingers")
		if ff, ok := loadCachedFingers(fingerCache); ok {
			set.RemoteFingers = ff.Len()
			if mode == ModeOverride {
				finalFullFingers = cloneFullFingers(ff)
			} else {
				finalFullFingers = mergeFullFingers(localFullFingers, ff)
			}
		} else if ff, err := loadRemoteFingers(ctx, opts.CyberhubURL, opts.APIKey); err != nil {
			set.RemoteFingersErr = err
		} else if ff.Len() > 0 {
			set.RemoteFingers = ff.Len()
			saveCachedFingers(fingerCache, ff)
			if mode == ModeOverride {
				finalFullFingers = cloneFullFingers(ff)
			} else {
				finalFullFingers = mergeFullFingers(localFullFingers, ff)
			}
		}

		tplCache := cachePath(opts.CyberhubURL, opts.APIKey, "neutron")
		if tpls, ok := loadCachedTemplates(tplCache); ok {
			set.RemoteNeutron = len(tpls)
			if mode == ModeOverride {
				finalTemplates = tpls
			} else {
				finalTemplates = mergeTemplates(finalTemplates, tpls)
			}
		} else if rt, err := loadRemoteTemplates(ctx, opts.CyberhubURL, opts.APIKey); err != nil {
			set.RemoteNeutronErr = err
		} else if rt.Len() > 0 {
			set.RemoteNeutron = rt.Len()
			saveCachedTemplates(tplCache, rt.Templates())
			if mode == ModeOverride {
				finalTemplates = rt.Templates()
			} else {
				finalTemplates = mergeTemplates(finalTemplates, rt.Templates())
			}
		}
	}

	finalFingers := finalFullFingers.Fingers()
	httpFingers, socketFingers := splitFingers(finalFingers)
	set.gogoConfigs["http"] = marshalJSON(httpFingers)
	set.gogoConfigs["socket"] = marshalJSON(socketFingers)
	set.gogoConfigs["neutron"] = marshalTemplates(finalTemplates)
	set.sprayConfigs["http"] = marshalJSON(httpFingers)
	set.sprayConfigs["socket"] = marshalJSON(socketFingers)
	set.zombieConfigs["http"] = marshalJSON(httpFingers)
	set.zombieConfigs["socket"] = marshalJSON(socketFingers)

	set.FingersConfig = fingers.NewConfig()
	set.FingersConfig.FullFingers = finalFullFingers
	set.NeutronConfig = neutron.NewConfig().WithTemplates(finalTemplates)

	set.Fingers, err = fingers.NewEngineWithFingers(finalFullFingers)
	if err != nil {
		return nil, err
	}
	set.Neutron, err = neutron.NewEngine(set.NeutronConfig)
	if err != nil {
		return nil, err
	}
	return set, nil
}

func NormalizeMode(mode string) (string, error) {
	mode = strings.ToLower(strings.TrimSpace(mode))
	if mode == "" {
		return ModeMerge, nil
	}
	switch mode {
	case ModeMerge, ModeOverride:
		return mode, nil
	default:
		return "", fmt.Errorf("invalid cyberhub mode %q: expected merge or override", mode)
	}
}

func defaultGogoConfigs() map[string][]byte {
	return map[string][]byte{
		"http":                   embeddedOrDefault(loadEmbeddedConfig, "http", []byte("[]")),
		"socket":                 embeddedOrDefault(loadEmbeddedConfig, "socket", []byte("[]")),
		"fingerprinthub_web":     embeddedOrDefault(loadEmbeddedConfig, "fingerprinthub_web", []byte("[]")),
		"fingerprinthub_service": embeddedOrDefault(loadEmbeddedConfig, "fingerprinthub_service", []byte("[]")),
		"port":                   embeddedOrDefault(loadEmbeddedConfig, "port", []byte("[]")),
		"extract":                embeddedOrDefault(loadEmbeddedConfig, "extract", []byte("[]")),
		"workflow":               embeddedOrDefault(loadEmbeddedConfig, "workflow", []byte("[]")),
		"neutron":                embeddedOrDefault(loadEmbeddedConfig, "neutron", []byte("[]")),
	}
}

func defaultSprayConfigs() map[string][]byte {
	m := map[string][]byte{
		"http":    embeddedOrDefault(loadEmbeddedConfig, "http", []byte("[]")),
		"socket":  embeddedOrDefault(loadEmbeddedConfig, "socket", []byte("[]")),
		"port":    embeddedOrDefault(loadEmbeddedConfig, "port", []byte("[]")),
		"extract": embeddedOrDefault(loadEmbeddedConfig, "extract", []byte("[]")),
	}
	// Include spray-specific keys when generated templates provide them.
	// When loadEmbeddedConfig returns nil (stub build), these entries are
	// omitted so that spray falls through to its own embedded defaults.
	for _, key := range []string{"spray_rule", "spray_dict", "spray_common"} {
		if data := loadEmbeddedConfig(key); len(data) > 0 {
			m[key] = data
		}
	}
	return m
}

func defaultZombieConfigs() map[string][]byte {
	m := map[string][]byte{
		"http":   embeddedOrDefault(loadEmbeddedConfig, "http", []byte("[]")),
		"socket": embeddedOrDefault(loadEmbeddedConfig, "socket", []byte("[]")),
		"port":   embeddedOrDefault(loadEmbeddedConfig, "port", []byte("[]")),
	}
	// Include zombie-specific keys when generated templates provide them.
	// When loadEmbeddedConfig returns nil (stub build), these entries are
	// omitted so that zombie falls through to its own embedded defaults.
	for _, key := range []string{"zombie_common", "zombie_default", "zombie_rule", "zombie_template"} {
		if data := loadEmbeddedConfig(key); len(data) > 0 {
			m[key] = data
		}
	}
	return m
}

func embeddedOrDefault(provider func(string) []byte, name string, fallback []byte) []byte {
	if provider == nil {
		return fallback
	}
	if data := provider(name); len(data) > 0 {
		return data
	}
	return fallback
}

func loadLocalFingers() (fingerslib.Fingers, fingerslib.Fingers, error) {
	httpFingers, err := fingerslib.LoadFingers(loadEmbeddedConfig("http"))
	if err != nil {
		return nil, nil, err
	}
	socketFingers, err := fingerslib.LoadFingers(loadEmbeddedConfig("socket"))
	if err != nil {
		return nil, nil, err
	}
	return httpFingers, socketFingers, nil
}

func loadLocalTemplates() []*templates.Template {
	content := loadEmbeddedConfig("neutron")
	if len(content) == 0 {
		return nil
	}
	var tpls []*templates.Template
	if err := yaml.Unmarshal(content, &tpls); err != nil {
		return nil
	}
	return tpls
}

func installLocalPortPreset() error {
	content := loadEmbeddedConfig("port")
	if len(content) == 0 {
		return nil
	}
	fingerresources.PortData = cloneBytes(content)
	var ports []*utils.PortConfig
	if err := yaml.Unmarshal(content, &ports); err != nil {
		return err
	}
	preset := utils.NewPortPreset(ports)
	utils.PrePort = preset
	fingerresources.PrePort = preset
	return nil
}

func loadRemoteFingers(ctx context.Context, cyberhubURL, apiKey string) (fingers.FullFingers, error) {
	provider := cyberhub.NewProvider(cyberhubURL, apiKey).WithTimeout(60 * time.Second)
	config := fingers.NewConfig().WithProvider(provider)
	if err := config.Load(ctx); err != nil {
		return fingers.FullFingers{}, err
	}
	return config.FullFingers, nil
}

func loadRemoteTemplates(ctx context.Context, cyberhubURL, apiKey string) (neutron.Templates, error) {
	provider := cyberhub.NewProvider(cyberhubURL, apiKey).WithTimeout(60 * time.Second)
	config := neutron.NewConfig().WithProvider(provider)
	if err := config.Load(ctx); err != nil {
		return neutron.Templates{}, err
	}
	return config.Templates, nil
}

func cloneFullFingers(src fingers.FullFingers) fingers.FullFingers {
	if src.Len() == 0 {
		return fingers.FullFingers{}
	}
	out := fingers.FullFingers{Items: make(map[string]*fingers.FullFinger, len(src.Items))}
	for key, item := range src.Items {
		out.Items[key] = item
	}
	return out
}

func mergeFullFingers(local, remote fingers.FullFingers) fingers.FullFingers {
	out := cloneFullFingers(local)
	for _, item := range remote.Items {
		out = out.Append(item)
	}
	return out
}

func splitFingers(items fingerslib.Fingers) (fingerslib.Fingers, fingerslib.Fingers) {
	httpFingers := make(fingerslib.Fingers, 0)
	socketFingers := make(fingerslib.Fingers, 0)
	for _, item := range items {
		if item == nil {
			continue
		}
		switch item.Protocol {
		case "", fingerslib.HTTPProtocol:
			httpFingers = append(httpFingers, item)
		case fingerslib.TCPProtocol:
			socketFingers = append(socketFingers, item)
		}
	}
	return httpFingers, socketFingers
}

func mergeTemplates(local, remote []*templates.Template) []*templates.Template {
	if len(local) == 0 {
		return append([]*templates.Template(nil), remote...)
	}
	items := make(map[string]*templates.Template, len(local)+len(remote))
	for _, item := range local {
		if key := templateKey(item); key != "" {
			items[key] = item
		}
	}
	for _, item := range remote {
		if key := templateKey(item); key != "" {
			items[key] = item
		}
	}
	out := make([]*templates.Template, 0, len(items))
	for _, item := range items {
		out = append(out, item)
	}
	return out
}

func templateKey(item *templates.Template) string {
	if item == nil {
		return ""
	}
	if item.Info.Name != "" {
		return item.Info.Name
	}
	return item.Id
}

func marshalJSON(v any) []byte {
	data, err := json.Marshal(v)
	if err != nil || len(data) == 0 {
		return []byte("[]")
	}
	return data
}

func marshalTemplates(tpls []*templates.Template) []byte {
	if len(tpls) == 0 {
		return []byte("[]")
	}
	data, err := yaml.Marshal(tpls)
	if err != nil || len(data) == 0 {
		return []byte("[]")
	}
	return data
}

// GogoConfig returns gogo config bytes by logical template name.
func (s *Set) GogoConfig(name string) []byte {
	if s == nil {
		return nil
	}
	return cloneBytes(s.gogoConfigs[name])
}

// SprayConfig returns spray config bytes by logical template name.
func (s *Set) SprayConfig(name string) []byte {
	if s == nil {
		return nil
	}
	return cloneBytes(s.sprayConfigs[name])
}

// ZombieConfig returns zombie config bytes by logical template name.
func (s *Set) ZombieConfig(name string) []byte {
	if s == nil {
		return nil
	}
	return cloneBytes(s.zombieConfigs[name])
}

// ProtonConfig returns proton/found template data by category name.
// Used as ResourceProvider for sdk/proton.Config.
func ProtonConfig(name string) []byte {
	return loadEmbeddedConfig(name)
}

func cloneBytes(data []byte) []byte {
	if len(data) == 0 {
		return nil
	}
	out := make([]byte, len(data))
	copy(out, data)
	return out
}
