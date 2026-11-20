package passive

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"strconv"

	"github.com/chainreactors/cyber/core/telemetry"
	"github.com/projectdiscovery/uncover/sources"
)

const (
	fofaURL    = "https://fofa.info/api/v1/search/all"
	fofaFields = "ip,port,host,domain,title,icp"
	fofaSize   = 100
)

type fofaAgent struct{}

func (a *fofaAgent) Name() string { return "fofa" }

func (a *fofaAgent) Query(ctx context.Context, session *sources.Session, query *sources.Query) (chan sources.Result, error) {
	if session.Keys.FofaKey == "" {
		return nil, errors.New("empty fofa key")
	}
	results := make(chan sources.Result)
	telemetry.SafeGo("uncover-fofa", func() {
		defer close(results)
		var total int
		for page := 1; ; page++ {
			if ctx.Err() != nil {
				return
			}
			b64q := base64.StdEncoding.EncodeToString([]byte(query.Query))
			values := url.Values{"key": {session.Keys.FofaKey}, "qbase64": {b64q}, "fields": {fofaFields}, "page": {strconv.Itoa(page)}, "size": {strconv.Itoa(fofaSize)}, "full": {"false"}}
			req, err := sources.NewHTTPRequest(ctx, http.MethodGet, fofaURL+"?"+values.Encode(), nil)
			if err != nil {
				sources.SendResult(ctx, results, sources.Result{Source: a.Name(), Error: err})
				return
			}
			req.Header.Set("Accept", "application/json")
			resp, err := session.Do(req, a.Name())
			if err != nil {
				if resp != nil {
					resp.Body.Close()
				}
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
				raw := Fofa{pad(row, 0), pad(row, 1), pad(row, 2), pad(row, 3), pad(row, 4), pad(row, 5)}
				rawBytes, _ := json.Marshal(raw)
				port, _ := strconv.Atoi(raw.Port)
				r := sources.Result{Source: a.Name(), IP: raw.IP, Port: port, Host: raw.Host, Raw: rawBytes}
				if !sources.SendResult(ctx, results, r) {
					return
				}
			}
			total += len(body.Results)
			if body.Size == 0 || total >= query.Limit || len(body.Results) == 0 || total >= body.Size {
				return
			}
		}
	})
	return results, nil
}

type fofaResponse struct {
	Error   bool       `json:"error"`
	ErrMsg  string     `json:"errmsg"`
	Results [][]string `json:"results"`
	Size    int        `json:"size"`
}

// Fofa is the per-result payload stored in Result.Raw.
type Fofa struct {
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
