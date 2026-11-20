package passive

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"strconv"
	"strings"

	"github.com/chainreactors/cyber/core/telemetry"
	"github.com/projectdiscovery/uncover/sources"
)

const (
	hunterURL  = "https://hunter.qianxin.com/openApi/search?api-key=%s&search=%s&page=%d&page_size=%d&is_web=%d&start_time=%s&end_time=%s"
	hunterSize = 100
)

type hunterAgent struct{}

func (a *hunterAgent) Name() string { return "hunter" }

func (a *hunterAgent) Query(ctx context.Context, session *sources.Session, query *sources.Query) (chan sources.Result, error) {
	if session.Keys.HunterToken == "" {
		return nil, errors.New("empty hunter keys")
	}
	results := make(chan sources.Result)
	telemetry.SafeGo("uncover-hunter", func() {
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
				if resp != nil {
					resp.Body.Close()
				}
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
				raw := Hunter{
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
	})
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

// Hunter is the per-result payload stored in Result.Raw.
type Hunter struct {
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
