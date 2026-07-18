package webagent

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"time"

	cfg "github.com/chainreactors/aiscan/core/config"
	"github.com/chainreactors/aiscan/pkg/webproto"
)

func fetchRemoteConfig(webURL string) (*cfg.Option, error) {
	url := strings.TrimRight(webURL, "/") + "/api/config/distribute"
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	req, err := http.NewRequestWithContext(ctx, "GET", url, nil)
	if err != nil {
		return nil, fmt.Errorf("create request: %w", err)
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("fetch remote config: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("remote config: HTTP %d", resp.StatusCode)
	}

	var dc webproto.DistributeConfig
	if err := json.NewDecoder(resp.Body).Decode(&dc); err != nil {
		return nil, fmt.Errorf("decode remote config: %w", err)
	}
	return distributeToOption(&dc), nil
}

func distributeToOption(d *webproto.DistributeConfig) *cfg.Option {
	opt := &cfg.Option{
		LLMOptions: cfg.LLMOptions{
			Provider: d.LLM.Provider,
			BaseURL:  d.LLM.BaseURL,
			APIKey:   d.LLM.APIKey,
			Model:    d.LLM.Model,
			LLMProxy: d.LLM.Proxy,
		},
		ScannerOptions: cfg.ScannerOptions{
			CyberhubURL:  d.Cyberhub.URL,
			CyberhubKey:  d.Cyberhub.Key,
			CyberhubMode: d.Cyberhub.Mode,
			Proxy:        d.Cyberhub.Proxy,
		},
		AgentOptions: cfg.AgentOptions{
			Tools:       d.Agent.Tools,
			Timeout:     d.Agent.Timeout,
			SaveSession: d.Agent.SaveSession,
		},
		IOAOptions: cfg.IOAOptions{
			IOAURL:      d.IOA.URL,
			IOAToken:    d.IOA.Token,
			IOANodeName: d.IOA.NodeName,
			Space:       d.IOA.Space,
		},
		ScanConfig: cfg.ScanConfigOptions{
			Verify: d.Scan.Verify,
		},
	}
	opt.FofaEmail = d.Recon.FofaEmail
	opt.FofaKey = d.Recon.FofaKey
	opt.HunterToken = d.Recon.HunterToken
	opt.HunterAPIKey = d.Recon.HunterAPIKey
	opt.ReconProxy = d.Recon.Proxy
	opt.ReconLimit = d.Recon.Limit
	if d.Search.TavilyKeys != "" {
		cfg.DefaultTavilyKeys = cfg.ResolveString(cfg.DefaultTavilyKeys, d.Search.TavilyKeys)
	}
	return opt
}
