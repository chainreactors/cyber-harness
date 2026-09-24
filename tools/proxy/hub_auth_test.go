package proxy

import (
	"context"
	"encoding/base64"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"net/url"
	"sync/atomic"
	"testing"
)

func TestHubProxyAuthRejectsBeforeDial(t *testing.T) {
	for _, capture := range []bool{false, true} {
		t.Run(fmt.Sprintf("capture=%t", capture), func(t *testing.T) {
			target := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if r.Header.Get("Proxy-Authorization") != "" {
					t.Error("proxy credential reached the target")
				}
				fmt.Fprint(w, "target reached")
			})
			httpTarget := httptest.NewServer(target)
			defer httpTarget.Close()
			tlsTarget := httptest.NewTLSServer(target)
			defer tlsTarget.Close()
			hub, state, client := newTestHub(t, capture)
			httpsURL := tlsTarget.URL
			if capture {
				// The MITM certificate uses a hostname; relay mode validates the
				// target's test certificate, which includes its loopback IP.
				httpsURL = localhost(httpsURL)
			}
			var dials atomic.Int32
			state.SetAutoDial("test://auth", func(ctx context.Context, network, address string) (net.Conn, error) {
				dials.Add(1)
				return (&net.Dialer{}).DialContext(ctx, network, address)
			})
			wantAuth := "Basic " + base64.StdEncoding.EncodeToString([]byte("invocation:signature"))
			hub.SetProxyAuth(func(req *http.Request) error {
				if req.Header.Get("Proxy-Authorization") != wantAuth {
					return errors.New("invocation route required")
				}
				return nil
			})
			for _, endpoint := range []struct{ name, url string }{
				{"HTTP", httpTarget.URL}, {"CONNECT", httpsURL},
			} {
				for _, credentials := range []struct {
					name    string
					user    *url.Userinfo
					allowed bool
				}{
					{"missing", nil, false},
					{"invalid", url.UserPassword("invocation", "wrong"), false},
					{"valid", url.UserPassword("invocation", "signature"), true},
				} {
					t.Run(endpoint.name+"/"+credentials.name, func(t *testing.T) {
						proxyURL, err := url.Parse(hub.ProxyURL())
						if err != nil {
							t.Fatal(err)
						}
						proxyURL.User = credentials.user
						transport := client.Transport.(*http.Transport).Clone()
						transport.Proxy = http.ProxyURL(proxyURL)
						transport.TLSClientConfig.RootCAs = transport.TLSClientConfig.RootCAs.Clone()
						transport.TLSClientConfig.RootCAs.AddCert(tlsTarget.Certificate())
						connectStatus := 0
						transport.OnProxyConnectResponse = func(_ context.Context, _ *url.URL, _ *http.Request, response *http.Response) error {
							connectStatus = response.StatusCode
							return nil
						}
						defer transport.CloseIdleConnections()
						requestClient := &http.Client{Transport: transport, Timeout: client.Timeout}
						dials.Store(0)
						response, err := requestClient.Get(endpoint.url)
						status := connectStatus
						var body []byte
						if response != nil {
							status = response.StatusCode
							body, _ = io.ReadAll(response.Body)
							response.Body.Close()
						}
						if credentials.allowed {
							if err != nil || status != http.StatusOK || string(body) != "target reached" || dials.Load() == 0 {
								t.Fatalf("authorized request: status=%d body=%q dials=%d err=%v", status, body, dials.Load(), err)
							}
						} else if status != http.StatusProxyAuthRequired || dials.Load() != 0 {
							t.Fatalf("rejected request: status=%d dials=%d err=%v", status, dials.Load(), err)
						}
					})
				}
			}
			hub.SetProxyAuth(nil)
			if got := get(t, client, httpTarget.URL); got != "target reached" {
				t.Fatalf("clearing policy did not restore forwarding: %q", got)
			}
		})
	}
}

func TestHubProxyAuthIsInstanceScoped(t *testing.T) {
	target := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		fmt.Fprint(w, "allowed")
	}))
	defer target.Close()
	protected, _, protectedClient := newTestHub(t, false)
	protected.SetProxyAuth(func(*http.Request) error { return errors.New("denied") })
	_, _, publicClient := newTestHub(t, false)
	if got := get(t, publicClient, target.URL); got != "allowed" {
		t.Fatalf("policy affected a separate hub: %q", got)
	}
	response, err := protectedClient.Get(target.URL)
	if err != nil {
		t.Fatal(err)
	}
	response.Body.Close()
	if response.StatusCode != http.StatusProxyAuthRequired {
		t.Fatalf("protected hub status = %d", response.StatusCode)
	}
	// A callback may replace its own policy; the hub must not hold authMu
	// while invoking embedding code.
	protected.SetProxyAuth(func(*http.Request) error {
		protected.SetProxyAuth(nil)
		return nil
	})
	if got := get(t, protectedClient, target.URL); got != "allowed" {
		t.Fatalf("replacement callback did not forward: %q", got)
	}
}
