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
	configs map[string]map[string][]byte
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
		configs: defaultConfigs(),
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
	httpData := marshalJSON(httpFingers)
	socketData := marshalJSON(socketFingers)
	for _, engine := range []string{"gogo", "spray", "zombie"} {
		set.configs[engine]["http"] = httpData
		set.configs[engine]["socket"] = socketData
	}
	set.configs["gogo"]["neutron"] = marshalTemplates(finalTemplates)

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

func defaultConfigs() map[string]map[string][]byte {
	shared := loadEngineConfigs("http", "socket", "port")
	return map[string]map[string][]byte{
		"gogo":   mergeConfigs(shared,
			"fingerprinthub_web", "fingerprinthub_service",
			"extract", "workflow", "neutron"),
		"spray":  mergeConfigs(shared, "extract", "spray_rule", "spray_dict", "spray_common"),
		"zombie": mergeConfigs(shared, "zombie_common", "zombie_default", "zombie_rule", "zombie_template"),
		"proton": loadEngineConfigs("found_keys", "found_spray", "found_filter_ext", "found_filter_dir"),
	}
}

func loadEngineConfigs(keys ...string) map[string][]byte {
	m := make(map[string][]byte, len(keys))
	for _, key := range keys {
		if data := loadEmbeddedConfig(key); len(data) > 0 {
			m[key] = data
		}
	}
	return m
}

func mergeConfigs(base map[string][]byte, extra ...string) map[string][]byte {
	m := make(map[string][]byte, len(base)+len(extra))
	for k, v := range base {
		m[k] = v
	}
	for _, key := range extra {
		if data := loadEmbeddedConfig(key); len(data) > 0 {
			m[key] = data
		}
	}
	return m
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

// Config returns config bytes for the given engine and template name.
// Falls back to embedded data when the Set is nil or the key is missing.
func (s *Set) Config(engine, name string) []byte {
	if s != nil {
		if m := s.configs[engine]; m != nil {
			if data := m[name]; len(data) > 0 {
				return cloneBytes(data)
			}
		}
	}
	return loadEmbeddedConfig(name)
}

func (s *Set) GogoConfig(name string) []byte   { return s.Config("gogo", name) }
func (s *Set) SprayConfig(name string) []byte  { return s.Config("spray", name) }
func (s *Set) ZombieConfig(name string) []byte { return s.Config("zombie", name) }
func (s *Set) ProtonConfig(name string) []byte { return s.Config("proton", name) }

func cloneBytes(data []byte) []byte {
	if len(data) == 0 {
		return nil
	}
	out := make([]byte, len(data))
	copy(out, data)
	return out
}
