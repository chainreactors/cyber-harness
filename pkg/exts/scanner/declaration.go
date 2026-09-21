package scanner

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"github.com/chainreactors/cyber/core/resource"
	types "github.com/chainreactors/cyber/core/types"
	cfg "github.com/chainreactors/cyber/pkg/config"
	"github.com/chainreactors/sdk/pkg/cyberhub"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"
)

const connectionTimeout = 20 * time.Second

var (
	FofaInfoEndpoint     = "https://fofa.info/api/v1/info/my"
	HunterSearchEndpoint = "https://hunter.qianxin.com/openApi/search"
)

func Declare(resources *resource.Registry) error {
	_, err := resource.Add[cfg.Connection](resources,
		cfg.Connection{Section: "cyberhub", Test: testCyberhubConnection},
		cfg.Connection{Section: "recon", Test: testReconConnections},
	)
	return err
}

func testCyberhubConnection(ctx context.Context, in, stored *types.DistributeConfig) []*types.ConnectionCheck {
	hubURL := fallbackString(in.GetCyberhub().GetUrl(), stored.GetCyberhub().GetUrl())
	key := fallbackString(in.GetCyberhub().GetKey(), stored.GetCyberhub().GetKey())
	return []*types.ConnectionCheck{connectionCheck("cyberhub", func() (string, error) {
		if strings.TrimSpace(hubURL) == "" {
			return "", fmt.Errorf("cyberhub url is empty")
		}
		probeCtx, cancel := context.WithTimeout(ctx, connectionTimeout)
		defer cancel()
		provider := cyberhub.NewProvider(hubURL, key).
			WithTimeout(connectionTimeout).
			WithFilter(&cyberhub.ExportFilter{Limit: 1})
		fingers, _, err := provider.Fingers(probeCtx)
		if err != nil {
			return "", err
		}
		return fmt.Sprintf("reachable - %d fingerprint(s) sampled", len(fingers)), nil
	})}
}

func testReconConnections(ctx context.Context, in, stored *types.DistributeConfig) []*types.ConnectionCheck {
	proxy := fallbackString(in.GetRecon().GetProxy(), stored.GetRecon().GetProxy())
	var checks []*types.ConnectionCheck
	if fofaKey := fallbackString(in.GetRecon().GetFofaKey(), stored.GetRecon().GetFofaKey()); strings.TrimSpace(fofaKey) != "" {
		checks = append(checks, connectionCheck("fofa", func() (string, error) {
			return testFofaConnection(ctx, fofaKey, proxy)
		}))
	}
	if hunterKey := fallbackString(in.GetRecon().GetHunterApiKey(), stored.GetRecon().GetHunterApiKey()); strings.TrimSpace(hunterKey) != "" {
		checks = append(checks, connectionCheck("hunter", func() (string, error) {
			return testHunterConnection(ctx, hunterKey, proxy)
		}))
	}
	if len(checks) == 0 {
		checks = append(checks, &types.ConnectionCheck{Name: "recon", Error: "no FOFA or Hunter credentials configured"})
	}
	return checks
}

func redactConnectionURL(err error) error {
	var urlErr *url.Error
	if errors.As(err, &urlErr) {
		if index := strings.IndexByte(urlErr.URL, '?'); index >= 0 {
			urlErr.URL = urlErr.URL[:index] + "?<redacted>"
		}
	}
	return err
}

func getConnectionJSON(ctx context.Context, endpoint, proxy string) ([]byte, error) {
	probeCtx, cancel := context.WithTimeout(ctx, connectionTimeout)
	defer cancel()
	req, err := http.NewRequestWithContext(probeCtx, http.MethodGet, endpoint, nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("Accept", "application/json")
	transport := &http.Transport{Proxy: http.ProxyFromEnvironment}
	if strings.TrimSpace(proxy) != "" {
		if proxyURL, parseErr := url.Parse(proxy); parseErr == nil {
			transport.Proxy = http.ProxyURL(proxyURL)
		}
	}
	client := &http.Client{Timeout: connectionTimeout, Transport: transport}
	resp, err := client.Do(req)
	if err != nil {
		return nil, redactConnectionURL(err)
	}
	defer resp.Body.Close()
	body, _ := io.ReadAll(io.LimitReader(resp.Body, 1<<16))
	if resp.StatusCode != http.StatusOK {
		detail := strings.TrimSpace(string(body))
		if len(detail) > 200 {
			detail = detail[:200] + "..."
		}
		return nil, fmt.Errorf("HTTP %d: %s", resp.StatusCode, detail)
	}
	return body, nil
}

func testFofaConnection(ctx context.Context, key, proxy string) (string, error) {
	body, err := getConnectionJSON(ctx, FofaInfoEndpoint+"?key="+url.QueryEscape(key), proxy)
	if err != nil {
		return "", err
	}
	var response struct {
		Error     bool        `json:"error"`
		Errmsg    string      `json:"errmsg"`
		Email     string      `json:"email"`
		Username  string      `json:"username"`
		FofaPoint json.Number `json:"fofa_point"`
	}
	if err := json.Unmarshal(body, &response); err != nil {
		return "", fmt.Errorf("parse FOFA response: %w", err)
	}
	if response.Error {
		if response.Errmsg != "" {
			return "", fmt.Errorf("%s", response.Errmsg)
		}
		return "", fmt.Errorf("FOFA rejected the key")
	}
	who := response.Username
	if who == "" {
		who = response.Email
	}
	if who == "" {
		who = "key valid"
	}
	if response.FofaPoint != "" {
		return fmt.Sprintf("%s - %s points", who, response.FofaPoint.String()), nil
	}
	return who, nil
}

func testHunterConnection(ctx context.Context, key, proxy string) (string, error) {
	now := time.Now()
	params := url.Values{}
	params.Set("api-key", key)
	params.Set("search", base64.URLEncoding.EncodeToString([]byte(`ip="1.1.1.1"`)))
	params.Set("page", "1")
	params.Set("page_size", "1")
	params.Set("is_web", "3")
	params.Set("start_time", now.AddDate(0, 0, -1).Format("2006-01-02 15:04:05"))
	params.Set("end_time", now.Format("2006-01-02 15:04:05"))
	body, err := getConnectionJSON(ctx, HunterSearchEndpoint+"?"+params.Encode(), proxy)
	if err != nil {
		return "", err
	}
	var response struct {
		Code    int    `json:"code"`
		Message string `json:"message"`
		Data    struct {
			Total int `json:"total"`
		} `json:"data"`
	}
	if err := json.Unmarshal(body, &response); err != nil {
		return "", fmt.Errorf("parse Hunter response: %w", err)
	}
	if response.Code != 200 {
		if response.Message != "" {
			return "", fmt.Errorf("code %d: %s", response.Code, response.Message)
		}
		return "", fmt.Errorf("Hunter returned code %d", response.Code)
	}
	return fmt.Sprintf("key valid - %d total", response.Data.Total), nil
}

func connectionCheck(name string, run func() (string, error)) *types.ConnectionCheck {
	started := time.Now()
	detail, err := run()
	result := &types.ConnectionCheck{Name: name, LatencyMs: time.Since(started).Milliseconds()}
	if err != nil {
		result.Error = err.Error()
		return result
	}
	result.Ok = true
	result.Detail = detail
	return result
}

func fallbackString(in, stored string) string {
	if strings.TrimSpace(in) != "" {
		return in
	}
	return stored
}
