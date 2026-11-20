package passive

// Config 提供 uncover 资产测绘引擎所需的凭证与默认行为。
type Config struct {
	FofaKey      string
	FofaBaseURL  string // FOFA-compatible API origin, optionally with a gateway path prefix.
	HunterAPIKey string
	Limit        int
	Proxy        string // 给 uncover 的全局出站代理 (http://, https://, socks5://, socks5h://)
	Credentials  map[string]string
}
