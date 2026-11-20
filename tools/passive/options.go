package passive

// ReconOptions 提供 uncover 资产测绘引擎所需的凭证与默认行为。
type ReconOptions struct {
	FofaKey      string
	FofaURL      string
	HunterAPIKey string
	Limit        int
	IngressProxy string // 给 uncover 的全局出站代理 (http://, https://, socks5://, socks5h://)
	Credentials  map[string]string
}
