package cmd

import (
	"fmt"
	"os"
	"path/filepath"
	"strconv"

	"github.com/gookit/config/v2"
	yamldrv "github.com/gookit/config/v2/yaml"
)

const defaultConfigName = "config.yaml"

func newConfigLoader() *config.Config {
	c := config.New("aiscan")
	c.WithOptions(func(opt *config.Options) {
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
	return c.Decode(v)
}

func findDefaultConfigFile() string {
	if _, err := os.Stat(defaultConfigName); err == nil {
		return defaultConfigName
	}
	if dir, err := os.UserConfigDir(); err == nil {
		p := filepath.Join(dir, "aiscan", defaultConfigName)
		if _, err := os.Stat(p); err == nil {
			return p
		}
	}
	return ""
}

func loadAndApplyConfig(option *Option) string {
	configPath := option.ConfigFile
	if configPath == "" {
		configPath = findDefaultConfigFile()
	}
	if configPath == "" {
		return ""
	}
	if _, err := os.Stat(configPath); err != nil {
		if option.ConfigFile != "" {
			fmt.Fprintf(os.Stderr, "warning: config file not found: %s\n", configPath)
		}
		return ""
	}

	var loaded Option
	if err := LoadConfig(configPath, &loaded); err == nil {
		mergeOption(option, &loaded)
		loadScanDefaults(configPath)
	}
	return configPath
}

func loadScanDefaults(filename string) {
	c := newConfigLoader()
	if err := c.LoadFiles(filename); err != nil {
		return
	}
	if v := c.String("scan.verify"); v != "" {
		DefaultVerify = v
	}
	if v := c.Int("scan.verify_timeout"); v > 0 {
		DefaultVerifyTimeout = strconv.Itoa(v)
	}
}

func mergeOption(dst, src *Option) {
	dst.Provider = resolveString(dst.Provider, src.Provider)
	dst.BaseURL = resolveString(dst.BaseURL, src.BaseURL)
	dst.APIKey = resolveString(dst.APIKey, src.APIKey)
	dst.Model = resolveString(dst.Model, src.Model)
	dst.Proxy = resolveString(dst.Proxy, src.Proxy)
	mergeVisionOptions(dst, src)
	dst.CyberhubURL = resolveString(dst.CyberhubURL, src.CyberhubURL)
	dst.CyberhubKey = resolveString(dst.CyberhubKey, src.CyberhubKey)
	dst.CyberhubMode = resolveString(dst.CyberhubMode, src.CyberhubMode)
	dst.ACPURL = resolveString(dst.ACPURL, src.ACPURL)
	dst.ACPNodeName = resolveString(dst.ACPNodeName, src.ACPNodeName)
	if (dst.Space == "" || dst.Space == "default") && src.Space != "" {
		dst.Space = src.Space
	}
	if (dst.ACPDB == "" || dst.ACPDB == "./ioa.db") && src.ACPDB != "" {
		dst.ACPDB = src.ACPDB
	}
}

func mergeVisionOptions(dst, src *Option) {
	if !dst.Vision && src.Vision {
		dst.Vision = true
	}
	dst.VisionProvider = resolveString(dst.VisionProvider, src.VisionProvider)
	dst.VisionBaseURL = resolveString(dst.VisionBaseURL, src.VisionBaseURL)
	dst.VisionAPIKey = resolveString(dst.VisionAPIKey, src.VisionAPIKey)
	dst.VisionModel = resolveString(dst.VisionModel, src.VisionModel)
	dst.VisionProxy = resolveString(dst.VisionProxy, src.VisionProxy)
}

func InitDefaultConfig() string {
	return defaultConfigTemplate
}

const defaultConfigTemplate = `# aiscan 配置文件
#
# 编译时: build.sh 读取此文件，通过 ldflags 将配置固化到二进制
# 运行时: aiscan 自动加载 ./config.yaml 或 ~/.config/aiscan/config.yaml
# 优先级: -c 自定义配置 > CLI 参数 > 默认 config.yaml > 编译时固化值
#
# 仅填写需要的字段，留空或删除的字段不会覆盖其他来源的值

# LLM Provider 配置
llm:
  # openai, deepseek, openrouter, ollama, groq, moonshot, anthropic
  provider: ""
  # API base URL（留空使用 provider 默认值）
  base_url: ""
  # API key（建议使用环境变量而非写入文件）
  api_key: ""
  # 模型名称
  model: ""
  # 访问 LLM API 的 HTTP proxy
  proxy: ""

# Vision 配置
# enabled: true 时启用 vision 工具。留空 provider 等字段时复用 llm 配置。
vision:
  enabled: false
  # openai, openrouter, ollama, etc.（留空时可从 base_url 推断）
  provider: ""
  base_url: ""
  api_key: ""
  model: ""
  proxy: ""

# Cyberhub 资源服务
cyberhub:
  url: ""
  key: ""
  # merge 或 override
  mode: ""

# ACP 协作
acp:
  url: ""
  db: ""
  node_name: ""
  space: ""

# 扫描默认值
scan:
  # auto, off, low, medium, high, critical
  verify: ""
  # 单次验证超时秒数（0 表示不覆盖）
  verify_timeout: 0

# 通用选项
misc:
  debug: false
  quiet: false
  no_color: false

# 以下仅 build.sh 使用
build:
  osarch: ""
  tags: ""
  output: dist
`
