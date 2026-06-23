package config

import (
	"fmt"
	"os"
	"path/filepath"
	"strconv"

	gkcfg "github.com/gookit/config/v2"
	yamldrv "github.com/gookit/config/v2/yaml"
)

const DefaultConfigName = "config.yaml"

func init() {
	gkcfg.WithOptions(func(opt *gkcfg.Options) {
		opt.DecoderConfig.TagName = "config"
		opt.ParseDefault = true
	})
	gkcfg.AddDriver(yamldrv.Driver)
}

func newConfigLoader() *gkcfg.Config {
	c := gkcfg.New("aiscan")
	c.WithOptions(func(opt *gkcfg.Options) {
		opt.DecoderConfig.TagName = "config"
	})
	c.AddDriver(yamldrv.Driver)
	return c
}

func LoadConfig(filename string, v interface{}) error {
	c := newConfigLoader()
	if err := c.LoadFiles(filename); err != nil {
		return err
	}
	if err := c.Decode(v); err != nil {
		return err
	}
	applyExplicitReconNumericOptions(c, v)
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

	var loaded Option
	if err := LoadConfig(configPath, &loaded); err != nil {
		return configPath, fmt.Errorf("load config %s: %w", configPath, err)
	}
	mergeOption(option, &loaded)
	if err := loadRuntimeDefaults(configPath); err != nil {
		return configPath, fmt.Errorf("load runtime defaults %s: %w", configPath, err)
	}
	return configPath, nil
}

func loadRuntimeDefaults(filename string) error {
	c := newConfigLoader()
	if err := c.LoadFiles(filename); err != nil {
		return err
	}
	if v := c.String("scan.verify"); v != "" {
		DefaultVerify = v
	}
	if v := c.Int("scan.verify_timeout"); v > 0 {
		DefaultVerifyTimeout = strconv.Itoa(v)
	}
	if v := c.String("search.tavily_keys"); v != "" {
		DefaultTavilyKeys = v
	}
	return nil
}

func mergeOption(dst, src *Option) {
	dst.Provider = ResolveString(dst.Provider, src.Provider)
	dst.BaseURL = ResolveString(dst.BaseURL, src.BaseURL)
	dst.APIKey = ResolveString(dst.APIKey, src.APIKey)
	dst.Model = ResolveString(dst.Model, src.Model)
	dst.LLMProxy = ResolveString(dst.LLMProxy, src.LLMProxy)
	dst.CyberhubURL = ResolveString(dst.CyberhubURL, src.CyberhubURL)
	dst.CyberhubKey = ResolveString(dst.CyberhubKey, src.CyberhubKey)
	dst.CyberhubMode = ResolveString(dst.CyberhubMode, src.CyberhubMode)
	dst.FofaEmail = ResolveString(dst.FofaEmail, src.FofaEmail)
	dst.FofaKey = ResolveString(dst.FofaKey, src.FofaKey)
	dst.HunterToken = ResolveString(dst.HunterToken, src.HunterToken)
	dst.HunterAPIKey = ResolveString(dst.HunterAPIKey, src.HunterAPIKey)
	dst.ReconProxy = ResolveString(dst.ReconProxy, src.ReconProxy)
	if dst.ReconLimit == nil && src.ReconLimit != nil {
		dst.ReconLimit = src.ReconLimit
	}
	dst.Proxy = ResolveString(dst.Proxy, src.Proxy)
	dst.WebURL = ResolveString(dst.WebURL, src.WebURL)
	dst.IOAURL = ResolveString(dst.IOAURL, src.IOAURL)
	dst.IOAToken = ResolveString(dst.IOAToken, src.IOAToken)
	dst.IOANodeName = ResolveString(dst.IOANodeName, src.IOANodeName)
	if (dst.Space == "" || dst.Space == "default") && src.Space != "" {
		dst.Space = src.Space
	}
	if len(dst.Tools) == 0 && len(src.Tools) > 0 {
		dst.Tools = src.Tools
	}
	if !dst.SaveSession && src.SaveSession {
		dst.SaveSession = true
	}
	dst.DataDir = ResolveString(dst.DataDir, src.DataDir)
}

func InitDefaultConfig() string {
	return generateDefaultConfig()
}
