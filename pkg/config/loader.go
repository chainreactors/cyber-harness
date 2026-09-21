package config

import (
	"errors"
	"fmt"
	"gopkg.in/yaml.v3"
	"os"
	"reflect"

	gkcfg "github.com/gookit/config/v2"
	yamldrv "github.com/gookit/config/v2/yaml"
)

const DefaultConfigName = "cyber.yaml"

func newConfigLoader() *gkcfg.Config {
	c := gkcfg.New("cyber")
	c.WithOptions(func(opt *gkcfg.Options) {
		opt.DecoderConfig.TagName = "config"
		opt.ParseDefault = false
	})
	c.AddDriver(yamldrv.Driver)
	return c
}

func LoadConfig(filename string, v interface{}) error {
	c := newConfigLoader()
	if err := c.LoadFilesByFormat(gkcfg.Yaml, filename); err != nil {
		return err
	}
	return decodeConfig(c, v)
}

// LoadConfigBytes parses configuration bytes with the same flags-backed loader
// used for files, so callers that hold the content rather than a path still
// validate against the Option schema instead of a projection of it.
func LoadConfigBytes(data []byte, v interface{}) error {
	c := newConfigLoader()
	if err := c.LoadSources(gkcfg.Yaml, data); err != nil {
		return err
	}
	return decodeConfig(c, v)
}

func decodeConfig(c *gkcfg.Config, v interface{}) error {
	if err := c.Decode(v); err != nil {
		return err
	}
	applyExplicitReconNumericOptions(c, v)
	if option, ok := v.(*Option); ok {
		if option.Sections != nil {
			var err error
			option.Extensions, err = option.Sections.Normalize(c.Data())
			if err != nil {
				return err
			}
		}
		option.present = make(map[string]bool)
		visitOptions(option, func(field reflect.StructField, value reflect.Value, path string, _ bool) {
			if c.Exists(path) {
				option.present[path] = true
			}
		})
	}
	return nil
}

func applyExplicitReconNumericOptions(c *gkcfg.Config, v interface{}) {
	opt, ok := v.(*Option)
	if !ok || opt == nil {
		return
	}
	if c.Exists("recon.limit") {
		v := c.Int("recon.limit")
		opt.ReconLimit = &v
	}
}

func LoadAndApplyConfig(option *Option) (string, error) {
	snapshot, err := LoadSnapshot(option.Context, option.ConfigFile, option.Sections)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return "", err
		}
		return option.ConfigFile, err
	}
	option.Snapshot = snapshot
	data, err := yaml.Marshal(snapshot.RuntimeDocument(option.Sections))
	if err != nil {
		return "", err
	}
	loaded := Option{Sections: option.Sections}
	if err := LoadConfigBytes(data, &loaded); err != nil {
		return snapshot.Target, fmt.Errorf("load config %s: %w", snapshot.Target, err)
	}
	mergeOption(option, &loaded)
	if len(snapshot.Layers) == 0 {
		return "", nil
	}
	return snapshot.Target, nil
}

func InitDefaultConfig() string {
	return generateDefaultConfig()
}
