package proxy

import (
	"bufio"
	"context"
	"encoding/base64"
	"io"
	"net"
	"net/http"
	"strings"
	"sync"
	"time"

	"github.com/chainreactors/utils/mitmproxy/helper"
	log "github.com/sirupsen/logrus"
)

const peekTimeout = 200 * time.Millisecond

// parseProxyAuthUser returns the username from a "Basic" Proxy-Authorization
// header value, or "" when the value is absent, not Basic, or malformed. Only
// the username is returned; the password (if any) is ignored.
func parseProxyAuthUser(header string) string {
	const prefix = "basic "
	if len(header) < len(prefix) || !strings.EqualFold(header[:len(prefix)], prefix) {
		return ""
	}
	decoded, err := base64.StdEncoding.DecodeString(strings.TrimSpace(header[len(prefix):]))
	if err != nil {
		return ""
	}
	if i := strings.IndexByte(string(decoded), ':'); i >= 0 {
		return string(decoded[:i])
	}
	return string(decoded)
}

// wrap tcpListener for remote client
type wrapListener struct {
	net.Listener
	proxy *Proxy
}

func (l *wrapListener) Accept() (net.Conn, error) {
	c, err := l.Listener.Accept()
	if err != nil {
		return nil, err
	}

	proxy := l.proxy
	wc := newWrapClientConn(c, proxy)
	connCtx := newConnContext(wc, proxy)
	wc.connCtx = connCtx

	for _, addon := range proxy.Addons {
		addon.ClientConnected(connCtx.ClientConn)
	}

	return wc, nil
}

// wrap tcpConn for remote client
type wrapClientConn struct {
	net.Conn
	r       *bufio.Reader
	proxy   *Proxy
	connCtx *ConnContext

	closeMu   sync.Mutex
	closed    bool
	closeErr  error
	closeChan chan struct{}
}

func newWrapClientConn(c net.Conn, proxy *Proxy) *wrapClientConn {
	return &wrapClientConn{
		Conn:      c,
		r:         bufio.NewReader(c),
		proxy:     proxy,
		closeChan: make(chan struct{}),
	}
}

func (c *wrapClientConn) Peek(n int) ([]byte, error) {
	return c.r.Peek(n)
}

func (c *wrapClientConn) PeekBuffered() ([]byte, error) {
	return c.r.Peek(c.r.Buffered())
}

func (c *wrapClientConn) Read(data []byte) (int, error) {
	return c.r.Read(data)
}

func (c *wrapClientConn) Close() error {
	c.closeMu.Lock()
	if c.closed {
		c.closeMu.Unlock()
		return c.closeErr
	}
	log.Debugln("in wrapClientConn close", c.connCtx.ClientConn.Conn.RemoteAddr())

	c.closed = true
	c.closeErr = c.Conn.Close()
	c.closeMu.Unlock()
	close(c.closeChan)

	for _, addon := range c.proxy.Addons {
		addon.ClientDisconnected(c.connCtx.ClientConn)
	}

	if c.connCtx.ServerConn != nil && c.connCtx.ServerConn.Conn != nil {
		c.connCtx.ServerConn.Conn.Close()
	}

	return c.closeErr
}

// wrap tcpConn for remote server
type wrapServerConn struct {
	net.Conn
	proxy   *Proxy
	connCtx *ConnContext

	closeMu  sync.Mutex
	closed   bool
	closeErr error
}

func (c *wrapServerConn) Close() error {
	c.closeMu.Lock()
	if c.closed {
		c.closeMu.Unlock()
		return c.closeErr
	}
	log.Debugln("in wrapServerConn close", c.connCtx.ClientConn.Conn.RemoteAddr())

	c.closed = true
	c.closeErr = c.Conn.Close()
	c.closeMu.Unlock()

	for _, addon := range c.proxy.Addons {
		addon.ServerDisconnected(c.connCtx)
	}

	// HTTP upstream connections may end independently of downstream
	// keep-alive requests. Closing the client's read side here races the next
	// request when a streamed response has already flushed its last bytes.
	if c.connCtx.ClientConn.Tls {
		// if keep-alive connection close
		if !c.connCtx.closeAfterResponse {
			c.connCtx.ClientConn.Conn.Close()
		}
	}

	return c.closeErr
}

type entry struct {
	proxy    *Proxy
	server   *http.Server
	listener net.Listener
}

func newEntry(proxy *Proxy) *entry {
	e := &entry{proxy: proxy}
	e.server = &http.Server{
		Addr:    proxy.Opts.Addr,
		Handler: e,
		ConnContext: func(ctx context.Context, c net.Conn) context.Context {
			return context.WithValue(ctx, connContextKey, c.(*wrapClientConn).connCtx)
		},
	}
	return e
}

func (e *entry) start() error {
	addr := e.server.Addr
	if addr == "" {
		addr = ":http"
	}
	ln, err := net.Listen("tcp", addr)
	if err != nil {
		return err
	}

	log.Infof("Proxy start listen at %v\n", e.server.Addr)
	pln := &wrapListener{
		Listener: ln,
		proxy:    e.proxy,
	}
	e.listener = pln
	return e.server.Serve(pln)
}

func (e *entry) close() error {
	return e.server.Close()
}

func (e *entry) shutdown(ctx context.Context) error {
	return e.server.Shutdown(ctx)
}

func (e *entry) ServeHTTP(res http.ResponseWriter, req *http.Request) {
	proxy := e.proxy

	log := log.WithFields(log.Fields{
		"in":   "Proxy.entry.ServeHTTP",
		"host": req.Host,
	})
	// Add entry proxy authentication
	if e.proxy.authProxy != nil {
		b, err := e.proxy.authProxy(res, req)
		if !b {
			log.Errorf("Proxy authentication failed: %s", err.Error())
			httpError(res, "", http.StatusProxyAuthRequired)
			return
		}
	}
	// Capture the proxy-auth username for this request so addons can
	// attribute its flow to the client that opened it
	// (e.g. an injected tool-call id). This is observational only — it never
	// rejects — and the credential is stripped before the request is forwarded
	// so it does not leak upstream.
	if connCtx, ok := req.Context().Value(connContextKey).(*ConnContext); ok {
		connCtx.ProxyAuthUser = parseProxyAuthUser(req.Header.Get("Proxy-Authorization"))
	}
	req.Header.Del("Proxy-Authorization")

	// proxy via connect tunnel
	if req.Method == "CONNECT" {
		e.handleConnect(res, req)
		return
	}

	if !req.URL.IsAbs() || req.URL.Host == "" {
		res = helper.NewResponseCheck(res)
		for _, addon := range proxy.Addons {
			addon.AccessProxyServer(req, res)
		}
		if res, ok := res.(*helper.ResponseCheck); ok {
			if !res.Wrote {
				res.WriteHeader(400)
				io.WriteString(res, "This is a proxy server, cannot make direct requests")
			}
		}
		return
	}

	// http proxy
	proxy.interceptor.initHttpDialFn(req)
	proxy.interceptor.attack(res, req)
}

func (e *entry) handleConnect(res http.ResponseWriter, req *http.Request) {
	proxy := e.proxy

	log := log.WithFields(log.Fields{
		"in":   "Proxy.entry.handleConnect",
		"host": req.Host,
	})

	shouldIntercept := proxy.shouldIntercept == nil || proxy.shouldIntercept(req)
	f := newFlow()
	f.Request = newRequest(req)
	f.ConnContext = req.Context().Value(connContextKey).(*ConnContext)
	f.ConnContext.Intercept = shouldIntercept
	defer f.finish()

	// trigger addon event Requestheaders
	for _, addon := range proxy.Addons {
		addon.Requestheaders(f)
	}

	if !shouldIntercept {
		log.Debugf("begin transpond %v", req.Host)
		e.directTransfer(res, req, f)
		return
	}

	if f.ConnContext.ClientConn.UpstreamCert {
		e.httpsDialFirstAttack(res, req, f)
		return
	}

	log.Debugf("begin intercept %v", req.Host)
	e.httpsDialLazyAttack(res, req, f)
}

func (e *entry) establishConnection(res http.ResponseWriter, f *Flow) (net.Conn, error) {
	cconn, _, err := res.(http.Hijacker).Hijack()
	if err != nil {
		f.Error = err
		for _, addon := range e.proxy.Addons {
			addon.HTTPConnectError(f, err)
		}
		res.WriteHeader(502)
		return nil, err
	}
	_, err = io.WriteString(cconn, "HTTP/1.1 200 Connection Established\r\n\r\n")
	if err != nil {
		cconn.Close()
		f.Error = err
		for _, addon := range e.proxy.Addons {
			addon.HTTPConnectError(f, err)
		}
		return nil, err
	}

	f.Response = &Response{
		StatusCode: 200,
		Header:     make(http.Header),
	}

	// trigger addon event Responseheaders
	for _, addon := range e.proxy.Addons {
		addon.Responseheaders(f)
	}

	return cconn, nil
}

func (e *entry) directTransfer(res http.ResponseWriter, req *http.Request, f *Flow) {
	proxy := e.proxy
	log := log.WithFields(log.Fields{
		"in":   "Proxy.entry.directTransfer",
		"host": req.Host,
	})

	conn, err := proxy.getUpstreamConn(req.Context(), req)
	if err != nil {
		f.Error = err
		for _, addon := range proxy.Addons {
			addon.HTTPConnectError(f, err)
		}
		res.WriteHeader(502)
		return
	}
	defer conn.Close()

	cconn, err := e.establishConnection(res, f)
	if err != nil {
		return
	}
	defer cconn.Close()

	transfer(log, conn, cconn)
}

// dispatchTunnel classifies the client data in a CONNECT tunnel and routes it
// to the appropriate handler. conn is the upstream server connection (may be nil
// for lazy-dial mode — dialFn will be called to establish it).
func (e *entry) dispatchTunnel(req *http.Request, f *Flow, cconn net.Conn, conn net.Conn, dialFn func() (net.Conn, error)) {
	proxy := e.proxy
	log := log.WithFields(log.Fields{"in": "Proxy.entry.dispatchTunnel", "host": req.Host})
	wc := cconn.(*wrapClientConn)

	closeBoth := func() {
		cconn.Close()
		if conn != nil {
			conn.Close()
		}
	}
	ensureConn := func() bool {
		if conn != nil {
			return true
		}
		var err error
		conn, err = dialFn()
		if err != nil {
			cconn.Close()
			log.Error(err)
			return false
		}
		return true
	}

	// If server connection exists, probe it for server-first protocols.
	// Server-first (SSH/SMTP/FTP): server sends data before client.
	if conn != nil {
		probe := make([]byte, 1)
		conn.SetReadDeadline(time.Now().Add(peekTimeout))
		n, _ := conn.Read(probe)
		conn.SetReadDeadline(time.Time{})
		if n > 0 {
			cconn.Write(probe[:1])
			transfer(log, conn, cconn)
			closeBoth()
			return
		}
	}

	// Always bound the client-side peek. For a server-first protocol whose
	// server probe above returned nothing (idle/slow server, or lazy mode with
	// no server conn yet) the client is itself waiting for the server and will
	// never send the bytes Peek wants — without a deadline Peek blocks forever,
	// a deadlock that surfaced as intermittent hangs under load. The timeout
	// lets it fall through to raw transfer below.
	wc.Conn.SetReadDeadline(time.Now().Add(peekTimeout))
	peek, err := wc.Peek(3)
	wc.Conn.SetReadDeadline(time.Time{})
	if err != nil {
		if !ensureConn() {
			return
		}
		transfer(log, conn, cconn)
		closeBoth()
		return
	}

	if helper.IsTls(peek) {
		f.ConnContext.ClientConn.Tls = true
		if conn != nil {
			proxy.interceptor.httpsTlsDial(req.Context(), cconn, conn)
		} else {
			proxy.interceptor.httpsLazyAttack(req.Context(), cconn, req)
		}
		return
	}

	wsPeek, _ := wc.PeekBuffered()
	if helper.IsWebSocket(wsPeek) {
		if !ensureConn() {
			return
		}
		if err := proxy.webSocketHandler.handle(conn, cconn, f); err != nil {
			log.Errorf("WebSocket handle error: %v", err)
			closeBoth()
		}
		return
	}

	if !ensureConn() {
		return
	}
	if helper.IsHTTPRequest(wsPeek) {
		proxy.interceptor.servePlainHTTP(cconn, conn)
		return
	}

	transfer(log, conn, cconn)
	closeBoth()
}

func (e *entry) httpsDialFirstAttack(res http.ResponseWriter, req *http.Request, f *Flow) {
	proxy := e.proxy

	conn, err := proxy.interceptor.httpsDial(req.Context(), req)
	if err != nil {
		f.Error = err
		for _, addon := range proxy.Addons {
			addon.HTTPConnectError(f, err)
		}
		res.WriteHeader(502)
		return
	}

	cconn, err := e.establishConnection(res, f)
	if err != nil {
		conn.Close()
		return
	}

	e.dispatchTunnel(req, f, cconn, conn, nil)
}

func (e *entry) httpsDialLazyAttack(res http.ResponseWriter, req *http.Request, f *Flow) {
	proxy := e.proxy

	cconn, err := e.establishConnection(res, f)
	if err != nil {
		return
	}

	e.dispatchTunnel(req, f, cconn, nil, func() (net.Conn, error) {
		return proxy.interceptor.httpsDial(req.Context(), req)
	})
}
