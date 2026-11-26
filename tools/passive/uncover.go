package passive

import (
	"context"
	"errors"
	"fmt"
	"maps"
	"net/url"
	"slices"
	"strings"
	"time"

	"github.com/projectdiscovery/uncover/sources"
)

// Uncover wraps uncover's session and agent infrastructure. It uses
// custom agents for fofa/hunter to preserve fields (title, icp, company,
// etc.) and stock uncover agents for other sources.
type Uncover struct {
	keys     sources.Keys
	proxy    string
	limit    int
	baseURLs map[string]string
	disabled []string
	keyless  []string
	secrets  []string
}

// NewUncover builds an engine from fully resolved Config. It does
// not read process environment variables; pkg/config owns that boundary.
func NewUncover(opts Config) *Uncover {
	p := &sources.Provider{}

	// Blank keys are not credentials, like the provider values below.
	opts.FofaKey = strings.TrimSpace(opts.FofaKey)
	opts.HunterAPIKey = strings.TrimSpace(opts.HunterAPIKey)
	if opts.FofaKey != "" {
		p.Fofa = append(p.Fofa, opts.FofaKey)
	}
	if opts.HunterAPIKey != "" {
		p.Hunter = append(p.Hunter, opts.HunterAPIKey)
	}

	applyCredentials(p, opts.Credentials)

	keys := p.GetKeys()
	// uncover currently expects a paired FOFA credential. Cyber's public
	// configuration is key-only, so populate the canonical key explicitly.
	if keys.FofaKey == "" && opts.FofaKey != "" {
		keys.FofaKey = opts.FofaKey
	}

	limit := opts.Limit
	if limit <= 0 {
		limit = 100
	}
	secrets := []string{opts.FofaKey, opts.HunterAPIKey}
	for _, value := range opts.Credentials {
		secrets = append(secrets, value)
	}
	return &Uncover{
		keys: keys, proxy: opts.Proxy, limit: limit,
		baseURLs: maps.Clone(opts.BaseURLs), disabled: slices.Clone(opts.Disabled),
		keyless: slices.Clone(opts.Keyless), secrets: secrets,
	}
}

func applyCredentials(p *sources.Provider, values map[string]string) {
	appendValue := func(dst *[]string, name string) {
		if value := strings.TrimSpace(values[name]); value != "" {
			*dst = append(*dst, value)
		}
	}
	appendPair := func(dst *[]string, first, second string) {
		left := strings.TrimSpace(values[first])
		right := strings.TrimSpace(values[second])
		if left != "" && right != "" {
			*dst = append(*dst, left+":"+right)
		}
	}

	appendValue(&p.Shodan, "SHODAN_API_KEY")
	appendValue(&p.Quake, "QUAKE_TOKEN")
	appendValue(&p.Netlas, "NETLAS_API_KEY")
	appendValue(&p.CriminalIP, "CRIMINALIP_API_KEY")
	appendValue(&p.Publicwww, "PUBLICWWW_API_KEY")
	appendValue(&p.HunterHow, "HUNTERHOW_API_KEY")
	appendValue(&p.ZoomEye, "ZOOMEYE_API_KEY")
	appendValue(&p.Driftnet, "DRIFTNET_API_KEY")
	appendValue(&p.Daydaymap, "DAYDAYMAP_API_KEY")
	appendValue(&p.Odin, "ODIN_API_KEY")
	appendValue(&p.BinaryEdge, "BINARYEDGE_API_KEY")
	appendValue(&p.Onyphe, "ONYPHE_API_KEY")
	appendValue(&p.GreyNoise, "GREYNOISE_API_KEY")
	appendValue(&p.NerdyData, "NERDYDATA_API_KEY")
	appendPair(&p.Censys, "CENSYS_API_TOKEN", "CENSYS_ORGANIZATION_ID")
	appendPair(&p.Google, "GOOGLE_API_KEY", "GOOGLE_API_CX")
}

// Sources returns the configured sources: those with credentials, plus keyless
// sources the host enabled explicitly. Nothing is available by default.
func (e *Uncover) Sources() []string {
	type check struct {
		name string
		ok   bool
	}
	checks := []check{
		{"fofa", e.keys.FofaKey != ""},
		{"hunter", e.keys.HunterToken != ""},
		{"shodan", e.keys.Shodan != ""},
		{"shodan-idb", slices.Contains(e.keyless, "shodan-idb")},
		{"censys", e.keys.CensysToken != ""},
		{"quake", e.keys.QuakeToken != ""},
		{"zoomeye", e.keys.ZoomEyeToken != ""},
		{"netlas", e.keys.NetlasToken != ""},
		{"criminalip", e.keys.CriminalIPToken != ""},
		{"publicwww", e.keys.PublicwwwToken != ""},
		{"hunterhow", e.keys.HunterHowToken != ""},
		{"odin", e.keys.OdinToken != ""},
		{"binaryedge", e.keys.BinaryEdgeToken != ""},
		{"onyphe", e.keys.OnypheKey != ""},
		{"driftnet", e.keys.DriftnetToken != ""},
		{"greynoise", e.keys.GreyNoiseKey != ""},
	}
	var out []string
	for _, c := range checks {
		if c.ok && !slices.Contains(e.disabled, c.name) {
			out = append(out, c.name)
		}
	}
	return out
}

// Query executes a single-source query and returns collected results.
func (e *Uncover) Query(ctx context.Context, src, query string) ([]sources.Result, error) {
	if !slices.Contains(e.Sources(), src) {
		return nil, fmt.Errorf("passive source %s is not configured", src)
	}
	agent, err := e.agentFor(src)
	if err != nil {
		return nil, err
	}
	session, err := sources.NewSession(&e.keys, 3, 600, 10, []string{src}, time.Minute, e.proxy)
	if err != nil {
		return nil, fmt.Errorf("uncover session: %w", err)
	}
	defer session.RateLimits.Stop()
	if baseURL := e.baseURLs[src]; baseURL != "" {
		if err := redirect(session, baseURL); err != nil {
			return nil, err
		}
	}
	defer session.Client.HTTPClient.CloseIdleConnections()
	if session.Client.HTTPClient2 != nil {
		defer session.Client.HTTPClient2.CloseIdleConnections()
	}
	ch, err := agent.Query(ctx, session, &sources.Query{Query: query, Limit: e.limit})
	if err != nil {
		return nil, fmt.Errorf("uncover query %s: %w", src, err)
	}
	var results []sources.Result
	var queryErr error
	for r := range ch {
		if r.Error != nil {
			var urlErr *url.Error
			if errors.As(r.Error, &urlErr) {
				copy := *urlErr
				copy.URL = "<redacted>"
				r.Error = &copy
			}
			message := r.Error.Error()
			for _, key := range e.secrets {
				if key != "" {
					message = strings.ReplaceAll(message, key, "<redacted>")
					message = strings.ReplaceAll(message, url.QueryEscape(key), "<redacted>")
				}
			}
			if message != r.Error.Error() {
				r.Error = errors.New(message)
			}
			queryErr = errors.Join(queryErr, r.Error)
			continue
		}
		r.Timestamp = time.Now().Unix()
		results = append(results, r)
	}
	return results, errors.Join(queryErr, ctx.Err())
}
