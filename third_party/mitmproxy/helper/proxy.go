package helper

import (
	"bufio"
	"context"
	"crypto/tls"
	"encoding/base64"
	"errors"
	"net"
	"net/http"
	"net/url"
	"strings"
	"time"

	"golang.org/x/net/proxy"
)

// GetProxyConn connect proxy
// ref: http/transport.go dialConn func
func GetProxyConn(ctx context.Context, proxyUrl *url.URL, address string, sslInsecure bool) (net.Conn, error) {
	var conn net.Conn
	if proxyUrl.Scheme == "socks5" {
		proxyAuth := &proxy.Auth{}
		if proxyUrl.User != nil {
			user := proxyUrl.User.Username()
			pass, _ := proxyUrl.User.Password()
			proxyAuth.User = user
			proxyAuth.Password = pass
		}
		dialer, err := proxy.SOCKS5("tcp", proxyUrl.Host, proxyAuth, proxy.Direct)
		if err != nil {
			return nil, err
		}
		dc := dialer.(interface {
			DialContext(ctx context.Context, network, addr string) (net.Conn, error)
		})
		conn, err = dc.DialContext(ctx, "tcp", address)
		if err != nil {
			conn.Close()
			return nil, err
		}
		return conn, err
	} else {
		conn, err := (&net.Dialer{}).DialContext(ctx, "tcp", proxyUrl.Host)
		if err != nil {
			return nil, err
		}
		if proxyUrl.Scheme == "https" {
			tlsConfig := &tls.Config{
				ServerName:         proxyUrl.Hostname(),
				InsecureSkipVerify: sslInsecure,
			}
			tlsConn := tls.Client(conn, tlsConfig)
			if err := tlsConn.HandshakeContext(ctx); err != nil {
				conn.Close()
				return nil, err
			}
			conn = tlsConn
		}
		connectReq := &http.Request{
			Method: "CONNECT",
			URL:    &url.URL{Opaque: address},
			Host:   address,
			Header: http.Header{},
		}
		if proxyUrl.User != nil {
			connectReq.Header.Set("Proxy-Authorization", "Basic "+base64.StdEncoding.EncodeToString([]byte(proxyUrl.User.String())))
		}
		connectCtx, cancel := context.WithTimeout(ctx, 1*time.Minute)
		defer cancel()
		didReadResponse := make(chan struct{})
		var resp *http.Response
		go func() {
			defer close(didReadResponse)
			err = connectReq.Write(conn)
			if err != nil {
				return
			}
			br := bufio.NewReader(conn)
			resp, err = http.ReadResponse(br, connectReq)
		}()
		select {
		case <-connectCtx.Done():
			conn.Close()
			<-didReadResponse
			return nil, connectCtx.Err()
		case <-didReadResponse:
		}
		if err != nil {
			conn.Close()
			return nil, err
		}
		if resp.StatusCode != 200 {
			_, text, ok := strings.Cut(resp.Status, " ")
			conn.Close()
			if !ok {
				return nil, errors.New("unknown status code")
			}
			return nil, errors.New(text)
		}
		return conn, nil
	}
}
