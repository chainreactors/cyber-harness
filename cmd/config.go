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

func init() {
	config.WithOptions(func(opt *config.Options) {
		opt.DecoderConfig.TagName = "config"
		opt.ParseDefault = true
	})
	config.AddDriver(yamldrv.Driver)
}

func intOption(v int) *int           { return &v }
func floatOption(v float64) *float64 { return &v }
func intOptionValue(p *int) int {
	if p != nil {
		return *p
	}
	return 0
}
func floatOptionValue(p *float64) float64 {
	if p != nil {
		return *p
	}
	return 0
}

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
	if err := c.Decode(v); err != nil {
		return err
	}
	applyExplicitReconNumericOptions(c, v)
	return nil
}

func applyExplicitReconNumericOptions(c *config.Config, v interface{}) {
	opt, ok := v.(*Option)
	if !ok || opt == nil {
		return
	}
	if c.Exists("recon.limit") {
		opt.ReconLimit = intOption(c.Int("recon.limit"))
	}
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

func loadAndApplyConfig(option *Option) (string, error) {
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
	applyScanDefaults(loaded.ScanConfig)
	return configPath, nil
}

func applyScanDefaults(sc scanConfigOptions) {
	if sc.Verify != "" {
		DefaultVerify = sc.Verify
	}
	if sc.VerifyTimeout > 0 {
		DefaultVerifyTimeout = strconv.Itoa(sc.VerifyTimeout)
	}
}

func mergeOption(dst, src *Option) {
	dst.Provider = resolveString(dst.Provider, src.Provider)
	dst.BaseURL = resolveString(dst.BaseURL, src.BaseURL)
	dst.APIKey = resolveString(dst.APIKey, src.APIKey)
	dst.Model = resolveString(dst.Model, src.Model)
	dst.LLMProxy = resolveString(dst.LLMProxy, src.LLMProxy)
	dst.CyberhubURL = resolveString(dst.CyberhubURL, src.CyberhubURL)
	dst.CyberhubKey = resolveString(dst.CyberhubKey, src.CyberhubKey)
	dst.CyberhubMode = resolveString(dst.CyberhubMode, src.CyberhubMode)
	dst.FofaEmail = resolveString(dst.FofaEmail, src.FofaEmail)
	dst.FofaKey = resolveString(dst.FofaKey, src.FofaKey)
	dst.HunterToken = resolveString(dst.HunterToken, src.HunterToken)
	dst.HunterAPIKey = resolveString(dst.HunterAPIKey, src.HunterAPIKey)
	dst.ReconProxy = resolveString(dst.ReconProxy, src.ReconProxy)
	if dst.ReconLimit == nil && src.ReconLimit != nil {
		dst.ReconLimit = src.ReconLimit
	}
	dst.ScannerOptions.Proxy = resolveString(dst.ScannerOptions.Proxy, src.ScannerOptions.Proxy)
	dst.IOAURL = resolveString(dst.IOAURL, src.IOAURL)
	dst.IOANodeName = resolveString(dst.IOANodeName, src.IOANodeName)
	if (dst.Space == "" || dst.Space == "default") && src.Space != "" {
		dst.Space = src.Space
	}
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
  # LLM API 代理
  proxy: ""

# Cyberhub 资源服务
cyberhub:
  url: ""
  key: ""
  # merge 或 override
  mode: ""
  # 扫描器代理（gogo, spray, zombie, neutron），如 socks5://127.0.0.1:1080
  proxy: ""

# IOA 协作
ioa:
  url: ""
  db: ""
  node_name: ""
  space: ""

# 资产测绘 (通过 uncover SDK)
# FOFA 凭证从此处或环境变量 FOFA_EMAIL / FOFA_KEY 读取
# 额外 source (Shodan/Censys/...) 通过环境变量或 ~/.uncover-config/provider-config.yaml 配置
recon:
  fofa_email: ""
  fofa_key: ""
  hunter_token: ""    # 极少用; Hunter web 端 token
  hunter_api_key: ""  # 华顺信安后台 API 管理生成的 64 位 hex key
  proxy: ""           # 出站代理 (Hunter 屏蔽境外 IP, 中国 VPS 走 socks5://host:1080)
  limit: 0            # 单次查询最多返回多少 asset, 0 = 不限

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
