package output

import (
	"net/url"
	"strconv"
	"strings"
)

// FormatAssetReport renders the terminal asset report. The sitemap tree is the
// terminal report's own feature; everything else comes from the shared
// renderer in report.go.
func FormatAssetReport(result *Result, color bool) string {
	return RenderReport(result, ReportOptions{
		Style:   StyleANSI,
		Color:   color,
		Sitemap: true,
	})
}

// --- shared asset helpers ---

func WebPath(rawURL string) string {
	parsed, err := url.Parse(strings.TrimSpace(rawURL))
	if err != nil || parsed.Scheme == "" || parsed.Host == "" {
		return FirstNonEmpty(rawURL, "/")
	}
	path := parsed.EscapedPath()
	if path == "" {
		path = "/"
	}
	if parsed.RawQuery != "" {
		path += "?" + parsed.RawQuery
	}
	return path
}

func HasTag(tags []string, tag string) bool {
	for _, t := range tags {
		if strings.EqualFold(t, tag) {
			return true
		}
	}
	return false
}

func CompactStrings(values ...string) []string {
	seen := make(map[string]struct{})
	out := make([]string, 0, len(values))
	for _, value := range values {
		value = strings.TrimSpace(value)
		if value == "" {
			continue
		}
		key := strings.ToLower(value)
		if _, ok := seen[key]; ok {
			continue
		}
		seen[key] = struct{}{}
		out = append(out, value)
	}
	return out
}

func AssetDataString(data map[string]any, key string) string {
	if len(data) == 0 {
		return ""
	}
	switch value := data[key].(type) {
	case string:
		return value
	case int:
		if value == 0 {
			return ""
		}
		return strconv.Itoa(value)
	case float64:
		if value == 0 {
			return ""
		}
		return strconv.Itoa(int(value))
	default:
		return ""
	}
}

func AssetDataInt(data map[string]any, key string) int {
	if len(data) == 0 {
		return 0
	}
	switch v := data[key].(type) {
	case int:
		return v
	case float64:
		return int(v)
	default:
		return 0
	}
}

func AssetDataStrings(data map[string]any, key string) []string {
	if len(data) == 0 {
		return nil
	}
	switch v := data[key].(type) {
	case []string:
		return v
	case []any:
		out := make([]string, 0, len(v))
		for _, item := range v {
			if s, ok := item.(string); ok && s != "" {
				out = append(out, s)
			}
		}
		return out
	default:
		return nil
	}
}
