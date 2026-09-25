package proxy

import (
	"context"
	"crypto/tls"
	"crypto/x509"
	"net"
	"net/http"
	"net/url"

	"github.com/chainreactors/utils/mitmproxy/cert"
	"github.com/chainreactors/utils/mitmproxy/helper"
	log "github.com/sirupsen/logrus"
)

type Options struct {
	Debug             int
	Addr              string
	StreamLargeBodies int64 // when request or response body is larger than this, switch to stream mode
	SslInsecure       bool
	CaRootPath        string
	NewCaFunc         func() (cert.CA, error)
	Upstream          string
	LogFilePath       string
	// Dialer, when set, is installed as the interceptor's custom dialer BEFORE
	// the plain-HTTP transport is built, so it applies to plain HTTP as well as
	// HTTPS/CONNECT flows. SetDialer, called after NewProxy, only affects the
	// tunneled paths because the plain-HTTP transport.DialContext is bound once
	// at construction. Prefer this field when the dialer must cover every flow.
	Dialer func(ctx context.Context, network, address string) (net.Conn, error)
}

type Proxy struct {
	Opts    *Options
	Version string
	Addons  []Addon

	entry            *entry
	interceptor      *interceptor
	webSocketHandler *webSocketHandler
	shouldIntercept  func(req *http.Request) bool
	upstreamProxy    func(req *http.Request) (*url.URL, error)
	authProxy        func(res http.ResponseWriter, req *http.Request) (bool, error)
	customDialer     func(ctx context.Context, network, address string) (net.Conn, error)
}

var proxyReqCtxKey = new(struct{})

func NewProxy(opts *Options) (*Proxy, error) {
	if opts.StreamLargeBodies <= 0 {
		opts.StreamLargeBodies = 1024 * 1024 * 5
	}

	proxy := &Proxy{
		Opts:    opts,
		Version: "1.9.2",
		Addons:  make([]Addon, 0),
	}

	// Install the custom dialer before newInterceptor so the plain-HTTP
	// transport's DialContext is bound to it too (SetDialer, being post-
	// construction, cannot retro-apply to that transport).
	if opts.Dialer != nil {
		proxy.customDialer = opts.Dialer
	}

	proxy.entry = newEntry(proxy)

	interceptor, err := newInterceptor(proxy)
	if err != nil {
		return nil, err
	}
	proxy.interceptor = interceptor
	proxy.webSocketHandler = newWebSocketHandler(proxy)

	return proxy, nil
}

func (proxy *Proxy) AddAddon(addon Addon) {
	proxy.Addons = append(proxy.Addons, addon)
}

func (proxy *Proxy) Start() error {
	go func() {
		if err := proxy.interceptor.start(); err != nil {
			log.Error(err)
		}
	}()
	return proxy.entry.start()
}

// StartAsync starts the proxy in a background goroutine and returns the
// actual listen address. Useful when Opts.Addr uses port 0.
func (proxy *Proxy) StartAsync() (net.Addr, <-chan error, error) {
	addr := proxy.Opts.Addr
	if addr == "" {
		addr = ":http"
	}
	ln, err := net.Listen("tcp", addr)
	if err != nil {
		return nil, nil, err
	}
	go func() {
		if err := proxy.interceptor.start(); err != nil {
			log.Error(err)
		}
	}()
	pln := &wrapListener{Listener: ln, proxy: proxy}
	proxy.entry.listener = pln
	errCh := make(chan error, 1)
	go func() {
		errCh <- proxy.entry.server.Serve(pln)
	}()
	return ln.Addr(), errCh, nil
}

func (proxy *Proxy) Close() error {
	return proxy.entry.close()
}

func (proxy *Proxy) Shutdown(ctx context.Context) error {
	return proxy.entry.shutdown(ctx)
}

func (proxy *Proxy) GetCertificate() x509.Certificate {
	return *proxy.interceptor.ca.GetRootCA()
}

func (proxy *Proxy) GetCertificateByCN(commonName string) (*tls.Certificate, error) {
	return proxy.interceptor.ca.GetCert(commonName)
}

func (proxy *Proxy) SetShouldInterceptRule(rule func(req *http.Request) bool) {
	proxy.shouldIntercept = rule
}

func (proxy *Proxy) SetUpstreamProxy(fn func(req *http.Request) (*url.URL, error)) {
	proxy.upstreamProxy = fn
}

// SetDialer sets a custom dialer for upstream connections, bypassing the
// built-in HTTP/SOCKS5 proxy handling. This enables proxyclient-based
// protocols (trojan, vless, vmess, hysteria2, anytls) as upstream.
func (proxy *Proxy) SetDialer(fn func(ctx context.Context, network, address string) (net.Conn, error)) {
	proxy.customDialer = fn
}

func (proxy *Proxy) realUpstreamProxy() func(*http.Request) (*url.URL, error) {
	return func(cReq *http.Request) (*url.URL, error) {
		req := cReq.Context().Value(proxyReqCtxKey).(*http.Request)
		return proxy.getUpstreamProxyUrl(req)
	}
}

func (proxy *Proxy) getUpstreamProxyUrl(req *http.Request) (*url.URL, error) {
	if proxy.upstreamProxy != nil {
		return proxy.upstreamProxy(req)
	}
	if len(proxy.Opts.Upstream) > 0 {
		return url.Parse(proxy.Opts.Upstream)
	}
	if proxy.customDialer != nil {
		return nil, nil
	}
	cReq := &http.Request{URL: &url.URL{Scheme: "https", Host: req.Host}}
	return http.ProxyFromEnvironment(cReq)
}

func (proxy *Proxy) getUpstreamConn(ctx context.Context, req *http.Request) (net.Conn, error) {
	proxyUrl, err := proxy.getUpstreamProxyUrl(req)
	if err != nil {
		return nil, err
	}
	var conn net.Conn
	address := helper.CanonicalAddr(req.URL)
	if proxyUrl != nil {
		conn, err = helper.GetProxyConn(ctx, proxyUrl, address, proxy.Opts.SslInsecure)
	} else if proxy.customDialer != nil {
		conn, err = proxy.customDialer(ctx, "tcp", address)
	} else {
		conn, err = (&net.Dialer{}).DialContext(ctx, "tcp", address)
	}
	return conn, err
}

func (proxy *Proxy) SetAuthProxy(fn func(res http.ResponseWriter, req *http.Request) (bool, error)) {
	proxy.authProxy = fn
}
