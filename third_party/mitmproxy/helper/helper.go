package helper

import (
	"bufio"
	"bytes"
	"encoding/json"
	"io"
	"net"
	"net/http"
	"net/url"
	"os"
	"strings"
)

// ReaderToBuffer tries to read Reader into buffer
// If limit is not reached, successfully read into buffer
// Otherwise buffer returns nil, and returns new Reader with unread state
func ReaderToBuffer(r io.Reader, limit int64) ([]byte, io.Reader, error) {
	buf := bytes.NewBuffer(make([]byte, 0))
	lr := io.LimitReader(r, limit)

	_, err := io.Copy(buf, lr)
	if err != nil {
		return nil, nil, err
	}

	if int64(buf.Len()) == limit {
		return nil, io.MultiReader(bytes.NewBuffer(buf.Bytes()), r), nil
	}

	return buf.Bytes(), nil, nil
}

func NewStructFromFile(filename string, v interface{}) error {
	data, err := os.ReadFile(filename)
	if err != nil {
		return err
	}
	if err := json.Unmarshal(data, v); err != nil {
		return err
	}
	return nil
}

var portMap = map[string]string{
	"http":   "80",
	"https":  "443",
	"socks5": "1080",
	"ws":     "80",
	"wss":    "443",
}

// CanonicalAddr returns url.Host but always with a ":port" suffix.
func CanonicalAddr(url *url.URL) string {
	port := url.Port()
	if port == "" {
		port = portMap[url.Scheme]
	}
	return net.JoinHostPort(url.Hostname(), port)
}

// IsTls checks if the buffer starts with a TLS record magic
func IsTls(buf []byte) bool {
	if buf[0] == 0x16 && buf[1] == 0x03 && buf[2] <= 0x03 {
		return true
	} else {
		return false
	}
}

func IsWebSocket(buf []byte) bool {
	req, err := http.ReadRequest(bufio.NewReader(bytes.NewReader(buf)))
	if err != nil {
		return false
	}

	upgrade := req.Header.Get("Upgrade")
	connection := req.Header.Get("Connection")

	return strings.ToLower(upgrade) == "websocket" &&
		strings.Contains(strings.ToLower(connection), "upgrade")
}

func IsHTTPRequest(buf []byte) bool {
	methods := []string{"GET ", "POST ", "PUT ", "DELETE ", "HEAD ", "OPTIONS ", "PATCH ", "TRACE "}
	for _, m := range methods {
		if len(buf) >= len(m) && string(buf[:len(m)]) == m {
			return true
		}
	}
	return false
}

type ResponseCheck struct {
	http.ResponseWriter
	Wrote bool
}

func NewResponseCheck(r http.ResponseWriter) http.ResponseWriter {
	return &ResponseCheck{
		ResponseWriter: r,
	}
}

func (r *ResponseCheck) WriteHeader(statusCode int) {
	r.Wrote = true
	r.ResponseWriter.WriteHeader(statusCode)
}

func (r *ResponseCheck) Write(bytes []byte) (int, error) {
	r.Wrote = true
	return r.ResponseWriter.Write(bytes)
}
