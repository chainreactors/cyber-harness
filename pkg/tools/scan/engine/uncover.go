//go:build full

package engine

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/chainreactors/aiscan/pkg/telemetry"
	"github.com/projectdiscovery/uncover/sources"
)

// UncoverEngine wraps uncover's session and agent infrastructure to provide
// a QueryRaw API compatible with the old ina-go engine. It uses custom agents
// for fofa/hunter to preserve rich fields (title, icp, company, etc.) and
// falls back to stock uncover agents for other sources.
type UncoverEngine struct {
	provider *sources.Provider
	keys     sources.Keys
	proxy    string
	limit    int
	timeout  int
	logger   telemetry.Logger
	avail    []string
}

// NewUncoverEngine builds an engine from ReconOptions credentials merged with
// environment-provided keys (for sources like shodan, censys, etc.).
func NewUncoverEngine(opts ReconOptions, logger telemetry.Logger) *UncoverEngine {
	if logger == nil {
		logger = telemetry.NopLogger()
	}
	p := &sources.Provider{}

	if opts.FofaEmail != "" && opts.FofaKey != "" {
		p.Fofa = append(p.Fofa, opts.FofaEmail+":"+opts.FofaKey)
	}
	if opts.HunterAPIKey != "" {
		p.Hunter = append(p.Hunter, opts.HunterAPIKey)
	} else if opts.HunterToken != "" {
		p.Hunter = append(p.Hunter, opts.HunterToken)
	}

	p.LoadProviderKeysFromEnv()

	keys := p.GetKeys()

	timeout := 600
	limit := opts.Limit
	if limit <= 0 {
		limit = 100
	}

	e := &UncoverEngine{
		provider: p,
		keys:     keys,
		proxy:    opts.IngressProxy,
		limit:    limit,
		timeout:  timeout,
		logger:   logger,
	}
	e.avail = e.detectSources()
	return e
}

func (e *UncoverEngine) detectSources() []string {
	type check struct {
		name string
		ok   bool
	}
	checks := []check{
		{"fofa", e.keys.FofaEmail != "" && e.keys.FofaKey != ""},
		{"hunter", e.keys.HunterToken != ""},
		{"shodan", e.keys.Shodan != ""},
		{"shodan-idb", true},
		{"censys", e.keys.CensysToken != ""},
		{"quake", e.keys.QuakeToken != ""},
		{"zoomeye", e.keys.ZoomEyeToken != ""},
		{"netlas", e.keys.NetlasToken != ""},
		{"criminalip", e.keys.CriminalIPToken != ""},
		{"publicwww", e.keys.PublicwwwToken != ""},
		{"hunterhow", e.keys.HunterHowToken != ""},
		{"binaryedge", e.keys.BinaryEdgeToken != ""},
		{"onyphe", e.keys.OnypheKey != ""},
		{"driftnet", e.keys.DriftnetToken != ""},
		{"greynoise", e.keys.GreyNoiseKey != ""},
	}
	var out []string
	for _, c := range checks {
		if c.ok {
			out = append(out, c.name)
		}
	}
	return out
}

// Sources returns the list of sources that have valid credentials.
func (e *UncoverEngine) Sources() []string { return e.avail }

// QueryRaw executes a single-source query and returns collected results.
func (e *UncoverEngine) QueryRaw(ctx context.Context, src, query string) ([]sources.Result, error) {
	agent, err := e.agentFor(src)
	if err != nil {
		return nil, err
	}
	session, err := sources.NewSession(&e.keys, 3, e.timeout, 10, []string{src}, time.Minute, e.proxy)
	if err != nil {
		return nil, fmt.Errorf("uncover session: %w", err)
	}
	ch, err := agent.Query(ctx, session, &sources.Query{Query: query, Limit: e.limit})
	if err != nil {
		return nil, fmt.Errorf("uncover query %s: %w", src, err)
	}
	var results []sources.Result
	for r := range ch {
		if r.Error != nil {
			e.logger.Warnf("uncover source=%s error=%v", src, r.Error)
			continue
		}
		r.Timestamp = time.Now().Unix()
		results = append(results, r)
	}
	return results, nil
}

// Close is a no-op; sessions are per-query.
func (e *UncoverEngine) Close() error { return nil }

func (e *UncoverEngine) agentFor(src string) (sources.Agent, error) {
	switch src {
	case "fofa":
		return &richFofaAgent{}, nil
	case "hunter":
		return &richHunterAgent{}, nil
	default:
		return stockAgent(src)
	}
}

// --------------- rich FOFA agent ------------------------------------------------

const (
	fofaURL    = "https://fofa.info/api/v1/search/all?key=%s&qbase64=%s&fields=%s&page=%d&size=%d&full=%t"
	fofaFields = "ip,port,host,domain,title,icp"
	fofaSize   = 100
)

type richFofaAgent struct{}

func (a *richFofaAgent) Name() string { return "fofa" }

func (a *richFofaAgent) Query(ctx context.Context, session *sources.Session, query *sources.Query) (chan sources.Result, error) {
	if session.Keys.FofaEmail == "" || session.Keys.FofaKey == "" {
		return nil, errors.New("empty fofa keys")
	}
	results := make(chan sources.Result)
	go func() {
		defer close(results)
		var total int
		for page := 1; ; page++ {
			if ctx.Err() != nil {
				return
			}
			b64q := base64.StdEncoding.EncodeToString([]byte(query.Query))
			u := fmt.Sprintf(fofaURL, session.Keys.FofaKey, b64q, fofaFields, page, fofaSize, false)
			req, err := sources.NewHTTPRequest(ctx, http.MethodGet, u, nil)
			if err != nil {
				sources.SendResult(ctx, results, sources.Result{Source: a.Name(), Error: err})
				return
			}
			req.Header.Set("Accept", "application/json")
			resp, err := session.Do(req, a.Name())
			if err != nil {
				sources.SendResult(ctx, results, sources.Result{Source: a.Name(), Error: err})
				return
			}
			var body fofaResponse
			err = json.NewDecoder(resp.Body).Decode(&body)
			resp.Body.Close()
			if err != nil {
				sources.SendResult(ctx, results, sources.Result{Source: a.Name(), Error: err})
				return
			}
			if body.Error {
				sources.SendResult(ctx, results, sources.Result{Source: a.Name(), Error: fmt.Errorf("fofa: %s", body.ErrMsg)})
				return
			}
			for _, row := range body.Results {
				raw := RawFofa{pad(row, 0), pad(row, 1), pad(row, 2), pad(row, 3), pad(row, 4), pad(row, 5)}
				rawBytes, _ := json.Marshal(raw)
				port, _ := strconv.Atoi(raw.Port)
				r := sources.Result{Source: a.Name(), IP: raw.IP, Port: port, Host: raw.Host, Raw: rawBytes}
				if !sources.SendResult(ctx, results, r) {
					return
				}
			}
			total += len(body.Results)
			if body.Size == 0 || total >= query.Limit || len(body.Results) == 0 || total > body.Size {
				return
			}
		}
	}()
	return results, nil
}

type fofaResponse struct {
	Error   bool       `json:"error"`
	ErrMsg  string     `json:"errmsg"`
	Results [][]string `json:"results"`
	Size    int        `json:"size"`
}

// RawFofa is the rich per-result payload stored in Result.Raw.
type RawFofa struct {
	IP     string `json:"ip"`
	Port   string `json:"port"`
	Host   string `json:"host"`
	Domain string `json:"domain"`
	Title  string `json:"title"`
	ICP    string `json:"icp"`
}

func pad(row []string, i int) string {
	if i < len(row) {
		return row[i]
	}
	return ""
}

// --------------- rich Hunter agent ----------------------------------------------

const (
	hunterURL  = "https://hunter.qianxin.com/openApi/search?api-key=%s&search=%s&page=%d&page_size=%d&is_web=%d&start_time=%s&end_time=%s"
	hunterSize = 100
)

type richHunterAgent struct{}

func (a *richHunterAgent) Name() string { return "hunter" }

func (a *richHunterAgent) Query(ctx context.Context, session *sources.Session, query *sources.Query) (chan sources.Result, error) {
	if session.Keys.HunterToken == "" {
		return nil, errors.New("empty hunter keys")
	}
	results := make(chan sources.Result)
	go func() {
		defer close(results)
		var total int
		for page := 1; ; page++ {
			if ctx.Err() != nil {
				return
			}
			b64q := base64.URLEncoding.EncodeToString([]byte(query.Query))
			u := fmt.Sprintf(hunterURL, session.Keys.HunterToken, b64q, page, hunterSize, 0, "", "")
			req, err := sources.NewHTTPRequest(ctx, http.MethodGet, u, nil)
			if err != nil {
				sources.SendResult(ctx, results, sources.Result{Source: a.Name(), Error: err})
				return
			}
			req.Header.Set("Accept", "application/json")
			resp, err := session.Do(req, a.Name())
			if err != nil {
				sources.SendResult(ctx, results, sources.Result{Source: a.Name(), Error: err})
				return
			}
			var body hunterResponse
			err = json.NewDecoder(resp.Body).Decode(&body)
			resp.Body.Close()
			if err != nil {
				sources.SendResult(ctx, results, sources.Result{Source: a.Name(), Error: err})
				return
			}
			if body.Code != http.StatusOK {
				sources.SendResult(ctx, results, sources.Result{Source: a.Name(), Error: fmt.Errorf("hunter: code=%d msg=%s", body.Code, body.Msg)})
				return
			}
			for _, item := range body.Data.Arr {
				raw := RawHunter{
					IP:      item.IP,
					Port:    strconv.Itoa(item.Port),
					URL:     item.URL,
					Domain:  item.Domain,
					Title:   item.WebTitle,
					ICP:     item.Number,
					Status:  strconv.Itoa(item.StatusCode),
					Company: item.Company,
					Frame:   joinComponents(item.Component),
				}
				rawBytes, _ := json.Marshal(raw)
				r := sources.Result{Source: a.Name(), IP: item.IP, Port: item.Port, Host: item.Domain, Raw: rawBytes}
				if !sources.SendResult(ctx, results, r) {
					return
				}
			}
			total += len(body.Data.Arr)
			if body.Data.Total == 0 || total >= query.Limit || len(body.Data.Arr) == 0 {
				return
			}
		}
	}()
	return results, nil
}

type hunterResponse struct {
	Code int `json:"code"`
	Data struct {
		Total int              `json:"total"`
		Arr   []hunterDataItem `json:"arr"`
	} `json:"data"`
	Msg string `json:"msg"`
}

type hunterDataItem struct {
	IP         string            `json:"ip"`
	Port       int               `json:"port"`
	Domain     string            `json:"domain"`
	URL        string            `json:"url"`
	WebTitle   string            `json:"web_title"`
	Number     string            `json:"number"`
	StatusCode int               `json:"status_code"`
	Company    string            `json:"company"`
	Component  []hunterComponent `json:"component"`
}

type hunterComponent struct {
	Name    string `json:"name"`
	Version string `json:"version"`
}

// RawHunter is the rich per-result payload stored in Result.Raw.
type RawHunter struct {
	IP      string `json:"ip"`
	Port    string `json:"port"`
	URL     string `json:"url"`
	Domain  string `json:"domain"`
	Title   string `json:"title"`
	ICP     string `json:"icp"`
	Status  string `json:"status"`
	Company string `json:"company"`
	Frame   string `json:"frame"`
}

func joinComponents(cs []hunterComponent) string {
	parts := make([]string, 0, len(cs))
	for _, c := range cs {
		if c.Version != "" {
			parts = append(parts, c.Name+"/"+c.Version)
		} else {
			parts = append(parts, c.Name)
		}
	}
	return strings.Join(parts, ",")
}

// --------------- stock agents ---------------------------------------------------

func stockAgent(name string) (sources.Agent, error) {
	// Lazy-import stock agents to avoid pulling all transitive deps into the
	// engine package. We use a simple registry map populated at init time in
	// uncover_agents.go.
	if a, ok := stockAgents[name]; ok {
		return a, nil
	}
	return nil, fmt.Errorf("uncover: unknown source %q", name)
}
