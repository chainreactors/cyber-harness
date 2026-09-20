package config

import (
	"fmt"
	"os"
	"path/filepath"
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
		visitOptions(option, func(field reflect.StructField, value reflect.Value, path string) {
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

func findDefaultConfigFile() string {
	// 1. 当前工作目录
	if _, err := os.Stat(DefaultConfigName); err == nil {
		return DefaultConfigName
	}
	// 2. 二进制所在目录
	if exe, err := os.Executable(); err == nil {
		p := filepath.Join(filepath.Dir(exe), DefaultConfigName)
		if _, err := os.Stat(p); err == nil {
			return p
		}
	}
	return ""
}

func LoadAndApplyConfig(option *Option) (string, error) {
	configPath := option.ConfigFile
	if configPath == "" {
		configPath = findDefaultConfigFile()
	}
	if configPath == "" {
		return "", nil
	}
	if _, err := os.Stat(configPath); err != nil {
		if option.ConfigFile == "" && os.IsNotExist(err) {
			return "", nil
		}
		return "", fmt.Errorf("config file %s: %w", configPath, err)
	}

	loaded := Option{Sections: option.Sections}
	if err := LoadConfig(configPath, &loaded); err != nil {
		return configPath, fmt.Errorf("load config %s: %w", configPath, err)
	}
	mergeOption(option, &loaded)
	return configPath, nil
}

func InitDefaultConfig() string {
	return generateDefaultConfig()
}
