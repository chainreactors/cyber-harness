package proxy

import (
	"context"
	"crypto/tls"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"net/url"
	"sync"
	"testing"
	"time"

	"github.com/chainreactors/proxyclient"
)

func TestPerCallEgressDoesNotSwapGlobalRoute(t *testing.T) {
	target := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = io.WriteString(w, "ok")
	}))
	defer target.Close()
	hub := NewProxyHub(NewState(""), nil, t.TempDir(), false, nil).ProxyHub
	resource := &Resource{ProxyHub: hub}
	if err := resource.Start(t.Context()); err != nil {
		t.Fatal(err)
	}
	defer resource.Close(context.Background())
	var mu sync.Mutex
	counts := [2]int{}
	requests := make([]func() error, 2)
	for index := range requests {
		index := index
		dial := proxyclient.Dial(func(ctx context.Context, network, address string) (net.Conn, error) {
			mu.Lock()
			counts[index]++
			mu.Unlock()
			return (&net.Dialer{}).DialContext(ctx, network, address)
		})
		route, _, release := hub.egress(t.Context(), dial)
		defer release()
		proxyURL, err := url.Parse(route)
		if err != nil {
			t.Fatal(err)
		}
		client := &http.Client{Transport: &http.Transport{Proxy: http.ProxyURL(proxyURL)}, Timeout: 5 * time.Second}
		requests[index] = func() error {
			response, err := client.Get(target.URL)
			if err != nil {
				return err
			}
			defer response.Body.Close()
			_, err = io.Copy(io.Discard, response.Body)
			return err
		}
	}
	var wg sync.WaitGroup
	errs := make([]error, 20)
	for i := range errs {
		wg.Add(1)
		go func(index int) {
			defer wg.Done()
			errs[index] = requests[index%2]()
		}(i)
	}
	wg.Wait()
	for _, err := range errs {
		if err != nil {
			t.Fatal(err)
		}
	}
	if counts[0] != 10 || counts[1] != 10 {
		t.Fatalf("per-call dials = %v, want [10 10]", counts)
	}
}

func TestPerCallHTTPSRouteUsesConnectToken(t *testing.T) {
	target := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = io.WriteString(w, "ok")
	}))
	defer target.Close()
	resource := NewProxyHub(NewState(""), nil, t.TempDir(), false, nil)
	if err := resource.Start(t.Context()); err != nil {
		t.Fatal(err)
	}
	defer resource.Close(context.Background())
	hub := resource.ProxyHub
	count := 0
	route, _, release := hub.egress(t.Context(), proxyclient.Dial(func(ctx context.Context, network, address string) (net.Conn, error) {
		count++
		return (&net.Dialer{}).DialContext(ctx, network, address)
	}))
	defer release()
	proxyURL, err := url.Parse(route)
	if err != nil {
		t.Fatal(err)
	}
	client := &http.Client{Transport: &http.Transport{
		Proxy: http.ProxyURL(proxyURL), TLSClientConfig: &tls.Config{InsecureSkipVerify: true},
	}}
	response, err := client.Get(target.URL)
	if err != nil {
		t.Fatal(err)
	}
	defer response.Body.Close()
	data, err := io.ReadAll(response.Body)
	if err != nil || string(data) != "ok" || count == 0 {
		t.Fatalf("HTTPS body=%q dials=%d err=%v", data, count, err)
	}
}
