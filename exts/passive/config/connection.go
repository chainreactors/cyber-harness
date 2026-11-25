package config

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"

	types "github.com/chainreactors/cyber/core/types"
	cfg "github.com/chainreactors/cyber/pkg/config"
)

const connectionTimeout = 20 * time.Second

const hunterBaseURL = "https://hunter.qianxin.com"

func testReconConnections(ctx context.Context, in, stored *types.DistributeConfig) []*types.ConnectionCheck {
	if checks, found := testGatewayConnections(ctx, in, stored, "hunter"); found {
		return checks
	}
	inRecon, storedRecon := connectionSection(in, ConfigKey), connectionSection(stored, ConfigKey)
	proxy := fallbackString(connectionString(inRecon, "proxy"), connectionString(storedRecon, "proxy"))
	var checks []*types.ConnectionCheck
	if hunterKey := fallbackString(connectionString(inRecon, "hunter_api_key"), connectionString(storedRecon, "hunter_api_key")); strings.TrimSpace(hunterKey) != "" {
		checks = append(checks, cfg.Check("hunter", func() (string, error) {
			return testHunterConnection(ctx, hunterKey, "", proxy)
		}))
	}
	if len(checks) == 0 {
		checks = append(checks, &types.ConnectionCheck{Name: "recon", Error: "no Hunter credentials configured"})
	}
	return checks
}

func testGatewayConnections(ctx context.Context, in, stored *types.DistributeConfig, source string) ([]*types.ConnectionCheck, bool) {
	candidates := connectionSection(in, "gateway")
	cached := connectionSection(stored, "gateway")
	if candidates == nil {
		candidates = cached
	}
	var checks []*types.ConnectionCheck
	found := false
	for name, raw := range candidates {
		fields, _ := raw.(map[string]any)
		kind := connectionString(fields, "kind")
		if kind == "" {
			kind = name
		}
		if kind != source {
			continue
		}
		found = true
		if fields["enabled"] == false {
			continue
		}
		previous, _ := cached[name].(map[string]any)
		previousKind := connectionString(previous, "kind")
		if previousKind == "" {
			previousKind = name
		}
		config := cfg.CloneDocument(fields)
		delete(config, "kind")
		delete(config, "enabled")
		if previousKind == kind {
			if config["key"] == nil || config["key"] == "" {
				config["key"] = previous["key"]
			}
			for _, field := range []string{"base_url", "proxy"} {
				if _, exists := config[field]; !exists && previous[field] != nil {
					config[field] = previous[field]
				}
			}
		}
		checks = append(checks, cfg.Check(name, func() (string, error) {
			section := apiSection(source)
			if source == "fofa" {
				section = FofaSection()
			}
			value, err := section.Decode(config)
			if err != nil {
				return "", err
			}
			if source == "fofa" {
				return testFofaConnection(ctx, *value.(*Fofa))
			}
			api := value.(*cfg.API)
			recon, saved := connectionSection(in, ConfigKey), connectionSection(stored, ConfigKey)
			key := fallbackString(api.Key, fallbackString(connectionString(recon, "hunter_api_key"), connectionString(saved, "hunter_api_key")))
			proxy := fallbackString(connectionString(recon, "proxy"), connectionString(saved, "proxy"))
			return testHunterConnection(ctx, key, api.BaseURL, proxy)
		}))
	}
	return checks, found
}

func connectionSection(config *types.DistributeConfig, key string) map[string]any {
	if config == nil {
		return nil
	}
	return cfg.ValuesFromProto(config.GetExtensions())[key]
}

func connectionString(section map[string]any, key string) string {
	value, _ := section[key].(string)
	return value
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

func testFofaConnection(ctx context.Context, config Fofa) (string, error) {
	proxy := ""
	if config.Proxy != nil {
		proxy = *config.Proxy
	}
	endpoint := strings.TrimRight(config.BaseURL, "/") + "/api/v1/info/my"
	body, err := getConnectionJSON(ctx, endpoint+"?key="+url.QueryEscape(config.Key), proxy)
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

func testHunterConnection(ctx context.Context, key, baseURL, proxy string) (string, error) {
	now := time.Now()
	params := url.Values{}
	params.Set("api-key", key)
	params.Set("search", base64.URLEncoding.EncodeToString([]byte(`ip="192.0.2.1"`)))
	params.Set("page", "1")
	params.Set("page_size", "1")
	params.Set("is_web", "3")
	params.Set("start_time", now.AddDate(0, 0, -1).Format("2006-01-02 15:04:05"))
	params.Set("end_time", now.Format("2006-01-02 15:04:05"))
	if baseURL == "" {
		baseURL = hunterBaseURL
	}
	body, err := getConnectionJSON(ctx, strings.TrimRight(baseURL, "/")+"/openApi/search?"+params.Encode(), proxy)
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

func fallbackString(in, stored string) string {
	if strings.TrimSpace(in) != "" {
		return in
	}
	return stored
}
