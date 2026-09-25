package proxy

import (
	"bufio"
	"crypto/tls"
	"net"
	"net/http"
	"net/url"
	"strings"

	"github.com/gorilla/websocket"
	log "github.com/sirupsen/logrus"
)

var wsHandshakeHeaders = map[string]struct{}{
	"Upgrade":                  {},
	"Connection":               {},
	"Sec-Websocket-Key":        {},
	"Sec-Websocket-Version":    {},
	"Sec-Websocket-Extensions": {},
	"Sec-Websocket-Protocol":   {},
}

func cloneHeaderWithoutWSHandshake(h http.Header) http.Header {
	out := make(http.Header, len(h))
	for k, vals := range h {
		if _, skip := wsHandshakeHeaders[http.CanonicalHeaderKey(k)]; skip {
			continue
		}
		if strings.EqualFold(k, "Host") {
			continue
		}
		cloned := make([]string, len(vals))
		copy(cloned, vals)
		out[k] = cloned
	}
	return out
}

type webSocketHandler struct {
	proxy *Proxy
}

func newWebSocketHandler(proxy *Proxy) *webSocketHandler {
	return &webSocketHandler{proxy: proxy}
}

type connResponseWriter struct {
	conn        net.Conn
	header      http.Header
	statusCode  int
	wroteHeader bool
}

func newConnResponseWriter(conn net.Conn) *connResponseWriter {
	return &connResponseWriter{
		conn:   conn,
		header: make(http.Header),
	}
}

func (w *connResponseWriter) Header() http.Header {
	return w.header
}

func (w *connResponseWriter) Write(data []byte) (int, error) {
	if !w.wroteHeader {
		w.WriteHeader(http.StatusOK)
	}
	return w.conn.Write(data)
}

func (w *connResponseWriter) WriteHeader(statusCode int) {
	w.wroteHeader = true
	w.statusCode = statusCode
}

func (w *connResponseWriter) Hijack() (net.Conn, *bufio.ReadWriter, error) {
	buf := bufio.NewReadWriter(bufio.NewReader(w.conn), bufio.NewWriter(w.conn))
	return w.conn, buf, nil
}

func (h *webSocketHandler) handle(serverConn, clientConn net.Conn, f *Flow) error {
	buf := bufio.NewReader(clientConn)
	clientReq, err := http.ReadRequest(buf)
	if err != nil {
		log.Errorf("Failed to read client handshake: %v", err)
		return err
	}

	log.Debugf("Client WebSocket handshake: %s %s", clientReq.Method, clientReq.URL.Path)

	dialer := &websocket.Dialer{
		NetDial: func(network, addr string) (net.Conn, error) {
			return serverConn, nil
		},
		HandshakeTimeout: 0,
	}

	serverURL := "ws://" + clientReq.Host + clientReq.URL.RequestURI()
	log.Debugf("Connecting to server: %s", serverURL)

	serverWS, _, err := dialer.Dial(serverURL, nil)
	if err != nil {
		log.Errorf("Failed to dial server: %v", err)
		return err
	}
	defer serverWS.Close()

	log.Debugf("Server WebSocket connected, subprotocol: %s", serverWS.Subprotocol())

	respWriter := newConnResponseWriter(clientConn)

	upgrader := &websocket.Upgrader{
		ReadBufferSize:  1024,
		WriteBufferSize: 1024,
		CheckOrigin: func(r *http.Request) bool {
			return true
		},
	}

	clientWS, err := upgrader.Upgrade(respWriter, clientReq, nil)
	if err != nil {
		log.Errorf("Failed to upgrade client connection: %v", err)
		return err
	}
	defer clientWS.Close()

	log.Debugf("Client WebSocket upgraded successfully")

	wsData := newWebSocketData()
	f.WebScoket = wsData

	for _, addon := range h.proxy.Addons {
		addon.WebSocketStart(f)
	}

	return h.forwardMessages(clientWS, serverWS, f)
}

func (h *webSocketHandler) forwardMessages(clientWS, serverWS *websocket.Conn, f *Flow) error {
	defer func() {
		for _, addon := range h.proxy.Addons {
			addon.WebSocketEnd(f)
		}
	}()

	errChan := make(chan error, 2)

	// client -> server
	go func() {
		defer func() {
			serverWS.WriteMessage(websocket.CloseMessage, websocket.FormatCloseMessage(websocket.CloseNormalClosure, ""))
			serverWS.Close()
		}()

		for {
			msgType, msg, err := clientWS.ReadMessage()
			if err != nil {
				if websocket.IsCloseError(err, websocket.CloseNormalClosure, websocket.CloseGoingAway) {
					errChan <- nil
					return
				}
				errChan <- err
				return
			}

			f.WebScoket.addMessage(msgType, msg, true)
			for _, addon := range h.proxy.Addons {
				addon.WebSocketMessage(f)
			}

			if err := serverWS.WriteMessage(msgType, msg); err != nil {
				log.Errorf("Client -> Server: Write error: %v", err)
				errChan <- err
				return
			}
		}
	}()

	// server -> client
	go func() {
		defer func() {
			clientWS.WriteMessage(websocket.CloseMessage, websocket.FormatCloseMessage(websocket.CloseNormalClosure, ""))
			clientWS.Close()
		}()

		for {
			msgType, msg, err := serverWS.ReadMessage()
			if err != nil {
				if websocket.IsCloseError(err, websocket.CloseNormalClosure, websocket.CloseGoingAway) {
					errChan <- nil
					return
				}
				errChan <- err
				return
			}

			f.WebScoket.addMessage(msgType, msg, false)
			for _, addon := range h.proxy.Addons {
				addon.WebSocketMessage(f)
			}

			if err := clientWS.WriteMessage(msgType, msg); err != nil {
				log.Errorf("Server -> Client: Write error: %v", err)
				errChan <- err
				return
			}
		}
	}()

	err := <-errChan
	return err
}

func (h *webSocketHandler) handleWSS(res http.ResponseWriter, req *http.Request) error {
	serverURL := "wss://" + req.Host + req.URL.RequestURI()
	log.Debugf("Connecting to WSS server: %s", serverURL)
	if parsedURL, err := url.Parse(serverURL); err == nil {
		req.URL = parsedURL
	}

	connCtx := req.Context().Value(connContextKey).(*ConnContext)

	plainConn, err := h.proxy.getUpstreamConn(req.Context(), req)
	if err != nil {
		log.Errorf("Failed to get upstream connection: %v", err)
		return err
	}

	serverConn := newServerConn()
	serverConn.Address = req.Host
	serverConn.Conn = &wrapServerConn{
		Conn:    plainConn,
		proxy:   h.proxy,
		connCtx: connCtx,
	}
	connCtx.ServerConn = serverConn

	for _, addon := range connCtx.proxy.Addons {
		addon.ServerConnected(connCtx)
	}

	dialer := &websocket.Dialer{
		NetDial: func(network, addr string) (net.Conn, error) {
			return serverConn.Conn, nil
		},
		TLSClientConfig: &tls.Config{
			InsecureSkipVerify: h.proxy.Opts.SslInsecure,
		},
	}

	upstreamHeader := cloneHeaderWithoutWSHandshake(req.Header)
	serverWS, _, err := dialer.Dial(serverURL, upstreamHeader)
	if err != nil {
		log.Errorf("Failed to dial WSS server: %v", err)
		return err
	}
	defer serverWS.Close()

	upgrader := &websocket.Upgrader{
		ReadBufferSize:  1024,
		WriteBufferSize: 1024,
		CheckOrigin: func(r *http.Request) bool {
			return true
		},
	}

	clientWS, err := upgrader.Upgrade(res, req, nil)
	if err != nil {
		log.Errorf("Failed to upgrade client connection: %v", err)
		return err
	}
	defer clientWS.Close()

	log.Debugf("Client WSS upgraded successfully")

	wsData := newWebSocketData()
	f := newFlow()
	f.Request = newRequest(req)
	f.ConnContext = connCtx
	f.WebScoket = wsData
	defer f.finish()

	for _, addon := range h.proxy.Addons {
		addon.WebSocketStart(f)
	}

	return h.forwardMessages(clientWS, serverWS, f)
}
