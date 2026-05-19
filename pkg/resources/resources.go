package resources

//go:generate go run ./templates_gen.go -t ../../templates -o template.go -embed

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"

	fingerslib "github.com/chainreactors/fingers/fingers"
	fingerresources "github.com/chainreactors/fingers/resources"
	"github.com/chainreactors/neutron/templates"
	"github.com/chainreactors/sdk/fingers"
	"github.com/chainreactors/sdk/neutron"
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
	finalFingers := append(fingerslib.Fingers(nil), localFingers...)
	finalTemplates := loadLocalTemplates()

	set := &Set{
		Mode:          mode,
		RemoteEnabled: opts.CyberhubURL != "" && opts.APIKey != "",
		gogoConfigs:   defaultGogoConfigs(),
		sprayConfigs:  defaultSprayConfigs(),
		zombieConfigs: defaultZombieConfigs(),
	}

	if set.RemoteEnabled {
		remoteFingers, err := loadRemoteFingers(ctx, opts.CyberhubURL, opts.APIKey)
		if err != nil {
			set.RemoteFingersErr = err
		} else if remoteFingers.Len() > 0 {
			set.RemoteFingers = remoteFingers.Len()
			if mode == ModeOverride {
				finalFingers = remoteFingers.Fingers()
			} else {
				finalFingers = mergeFingers(localFingers, remoteFingers.Fingers())
			}
		}

		remoteTemplates, err := loadRemoteTemplates(ctx, opts.CyberhubURL, opts.APIKey)
		if err != nil {
			set.RemoteNeutronErr = err
		} else if remoteTemplates.Len() > 0 {
			set.RemoteNeutron = remoteTemplates.Len()
			if mode == ModeOverride {
				finalTemplates = remoteTemplates.Templates()
			} else {
				finalTemplates = mergeTemplates(finalTemplates, remoteTemplates.Templates())
			}
		}
	}

	httpFingers, socketFingers := splitFingers(finalFingers)
	set.gogoConfigs["http"] = marshalJSON(httpFingers)
	set.gogoConfigs["socket"] = marshalJSON(socketFingers)
	set.gogoConfigs["neutron"] = marshalTemplates(finalTemplates)
	set.sprayConfigs["http"] = marshalJSON(httpFingers)
	set.sprayConfigs["socket"] = marshalJSON(socketFingers)
	set.zombieConfigs["http"] = marshalJSON(httpFingers)
	set.zombieConfigs["socket"] = marshalJSON(socketFingers)

	set.FingersConfig = fingers.NewConfig().WithFingers(finalFingers)
	set.NeutronConfig = neutron.NewConfig().WithTemplates(finalTemplates)

	set.Fingers, err = fingers.NewEngine(set.FingersConfig)
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
	config := fingers.NewConfig().WithCyberhub(cyberhubURL, apiKey)
	if err := config.Load(ctx); err != nil {
		return fingers.FullFingers{}, err
	}
	return config.FullFingers, nil
}

func loadRemoteTemplates(ctx context.Context, cyberhubURL, apiKey string) (neutron.Templates, error) {
	config := neutron.NewConfig().WithCyberhub(cyberhubURL, apiKey)
	if err := config.Load(ctx); err != nil {
		return neutron.Templates{}, err
	}
	return config.Templates, nil
}

func mergeFingers(local, remote fingerslib.Fingers) fingerslib.Fingers {
	if len(local) == 0 {
		return append(fingerslib.Fingers(nil), remote...)
	}
	items := make(map[string]*fingerslib.Finger, len(local)+len(remote))
	for _, item := range local {
		if item == nil || item.Name == "" {
			continue
		}
		items[item.Name] = item
	}
	for _, item := range remote {
		if item == nil || item.Name == "" {
			continue
		}
		items[item.Name] = item
	}
	out := make(fingerslib.Fingers, 0, len(items))
	for _, item := range items {
		out = append(out, item)
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

func cloneBytes(data []byte) []byte {
	if len(data) == 0 {
		return nil
	}
	out := make([]byte, len(data))
	copy(out, data)
	return out
}
