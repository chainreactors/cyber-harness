package config

import (
	"fmt"
	"reflect"
	"strings"
)

const configFileHeader = `# aiscan 配置文件
#
# 运行时: aiscan 自动加载 ./aiscan.yaml 或 <二进制所在目录>/aiscan.yaml
# 优先级: CLI 参数 > 环境变量 > 配置文件 > 默认值
# 生成:   aiscan --init
#
# 仅填写需要的字段，留空或删除的字段不会覆盖其他来源的值
#
# LLM 配置支持两种格式:
#   格式一 — 单 provider 简写（兼容旧配置）:
#     llm:
#       provider: deepseek
#       api_key: sk-...
#       model: deepseek-chat
#
#   格式二 — providers 列表（第一个为主 provider，其余为 fallback）:
#     llm:
#       providers:
#         - provider: deepseek
#           api_key: sk-...
#           model: deepseek-chat
#         - provider: openai
#           api_key: sk-...
#           model: gpt-4o
#
#   两种可混用：单字段设为主 provider，providers 列表设为 fallback

`

const configFileTail = `# 搜索
search:
  # Tavily API keys (逗号分隔，留空则 fallback 到 DuckDuckGo)
  tavily_keys: ""

# 以下仅 build.sh 使用
build:
  osarch: ""
  tags: ""
  output: dist
`

func generateDefaultConfig() string {
	var b strings.Builder
	b.WriteString(configFileHeader)
	b.WriteString(generateFromStruct(reflect.TypeOf(Option{}), reflect.ValueOf(Option{}), 0))
	b.WriteString("\n")
	b.WriteString(configFileTail)
	return b.String()
}

func generateFromStruct(t reflect.Type, v reflect.Value, indent int) string {
	var b strings.Builder
	prefix := strings.Repeat("  ", indent)

	for i := 0; i < t.NumField(); i++ {
		field := t.Field(i)
		fieldVal := v.Field(i)
		configTag := field.Tag.Get("config")
		if configTag == "" {
			continue
		}

		groupTag := field.Tag.Get("group")
		descTag := field.Tag.Get("description")
		defaultTag := field.Tag.Get("default")

		fieldType := field.Type
		if fieldType.Kind() == reflect.Ptr {
			fieldType = fieldType.Elem()
		}

		switch {
		case fieldType.Kind() == reflect.Struct && field.Anonymous:
			if groupTag != "" {
				b.WriteString(fmt.Sprintf("%s# %s\n", prefix, groupTag))
			}
			b.WriteString(fmt.Sprintf("%s%s:\n", prefix, configTag))
			b.WriteString(generateFromStruct(fieldType, fieldVal, indent+1))
			b.WriteString("\n")

		case fieldType.Kind() == reflect.Struct && !field.Anonymous:
			if descTag != "" {
				b.WriteString(fmt.Sprintf("%s# %s\n", prefix, descTag))
			}
			b.WriteString(fmt.Sprintf("%s%s:\n", prefix, configTag))
			b.WriteString(generateFromStruct(fieldType, fieldVal, indent+1))
			b.WriteString("\n")

		case fieldType.Kind() == reflect.Slice && fieldType.Elem().Kind() == reflect.Struct:
			b.WriteString(generateSliceOfStructComment(prefix, configTag, descTag, fieldType.Elem()))

		case fieldType.Kind() == reflect.Slice:
			b.WriteString(generateSliceComment(prefix, configTag, descTag, fieldType))

		default:
			if descTag != "" {
				b.WriteString(fmt.Sprintf("%s# %s\n", prefix, descTag))
			}
			val := formatValue(fieldType.Kind(), defaultTag)
			b.WriteString(fmt.Sprintf("%s%s: %s\n", prefix, configTag, val))
		}
	}
	return b.String()
}

func generateSliceOfStructComment(prefix, configTag, descTag string, elemType reflect.Type) string {
	var b strings.Builder
	if descTag != "" {
		b.WriteString(fmt.Sprintf("%s# %s\n", prefix, descTag))
	}
	b.WriteString(fmt.Sprintf("%s# %s:\n", prefix, configTag))
	b.WriteString(fmt.Sprintf("%s#   - ", prefix))
	first := true
	for j := 0; j < elemType.NumField(); j++ {
		f := elemType.Field(j)
		ct := f.Tag.Get("config")
		if ct == "" {
			continue
		}
		dt := f.Tag.Get("default")
		val := formatValue(f.Type.Kind(), dt)
		if first {
			b.WriteString(fmt.Sprintf("%s: %s\n", ct, val))
			first = false
		} else {
			b.WriteString(fmt.Sprintf("%s#     %s: %s\n", prefix, ct, val))
		}
	}
	return b.String()
}

func generateSliceComment(prefix, configTag, descTag string, t reflect.Type) string {
	var b strings.Builder
	if descTag != "" {
		b.WriteString(fmt.Sprintf("%s# %s\n", prefix, descTag))
	}
	b.WriteString(fmt.Sprintf("%s# %s: []\n", prefix, configTag))
	return b.String()
}

func formatValue(kind reflect.Kind, defaultVal string) string {
	if defaultVal != "" {
		switch kind {
		case reflect.String:
			return fmt.Sprintf("%q", defaultVal)
		default:
			return defaultVal
		}
	}
	switch kind {
	case reflect.Bool:
		return "false"
	case reflect.Int, reflect.Int8, reflect.Int16, reflect.Int32, reflect.Int64:
		return "0"
	case reflect.Float32, reflect.Float64:
		return "0.0"
	case reflect.String:
		return `""`
	default:
		return `""`
	}
}
