package passive

// Config 提供 uncover 资产测绘引擎所需的凭证与默认行为。
type Config struct {
	FofaKey      string
	BaseURLs     map[string]string // Per-source origins, optionally with a gateway path prefix.
	Disabled     []string
	Keyless      []string // 需要显式启用的免 key 来源（如 shodan-idb）；未列出时不可用
	HunterAPIKey string
	Limit        int
	Proxy        string // 给 uncover 的全局出站代理 (http://, https://, socks5://, socks5h://)
	Credentials  map[string]string
}
