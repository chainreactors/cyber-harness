package probe

// ProbeConfig holds the fields probe needs to test external connectivity.
// The web layer converts its own DistributeConfig into this before calling TestConn.
type ProbeConfig struct {
	Cyberhub CyberhubProbe
	Recon    ReconProbe
	Search   SearchProbe
	IOA      IOAProbe
}

type CyberhubProbe struct {
	URL string
	Key string
}

type ReconProbe struct {
	FofaKey      string
	HunterToken  string
	HunterAPIKey string
	Proxy        string
}

type SearchProbe struct {
	TavilyKeys string
}

type IOAProbe struct {
	URL   string
	Token string
}
