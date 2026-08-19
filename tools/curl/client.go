package curl

import (
	"context"
	"crypto/tls"
	"crypto/x509"
	"encoding/base64"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/http/cookiejar"
	"net/url"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	toolpb "github.com/chainreactors/aiscan/aop/tool"
)

// A single stable, modern Chrome identity. Keeping one fingerprint per process
// (rather than rotating per request) is itself the natural shape: a real client
// does not change its User-Agent between requests from the same egress. The
// version is aligned with the uTLS HelloChrome preset used at the hub upstream
// so the header story and the (future) TLS story agree.
const chromeUserAgent = "Mozilla/5.0 (X11; Linux x86_64) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/133.0.0.0 Safari/537.36"

// browserDefaults are added only when the caller has not set the same header.
// They complete the "looks like a browser navigation" shape without overriding
// anything the caller deliberately chose. Accept-Encoding is intentionally left
// to the transport (transparent gzip) so it is real rather than advertised.
var browserDefaults = []Header{
	{Name: "Accept", Value: "text/html,application/xhtml+xml,application/xml;q=0.9,image/avif,image/webp,image/apng,*/*;q=0.8,application/signed-exchange;v=b3;q=0.7"},
	{Name: "Accept-Language", Value: "en-US,en;q=0.9"},
	{Name: "Sec-Ch-Ua", Value: `"Chromium";v="133", "Google Chrome";v="133", "Not(A:Brand";v="24"`},
	{Name: "Sec-Ch-Ua-Mobile", Value: "?0"},
	{Name: "Sec-Ch-Ua-Platform", Value: `"Linux"`},
	{Name: "Sec-Fetch-Dest", Value: "document"},
	{Name: "Sec-Fetch-Mode", Value: "navigate"},
	{Name: "Sec-Fetch-Site", Value: "none"},
	{Name: "Sec-Fetch-User", Value: "?1"},
	{Name: "Upgrade-Insecure-Requests", Value: "1"},
}

// do runs one parsed curl request end to end: builds the client (routing through
// the runner's MITM hub when the environment provides it), applies the browser
// naturalization defaults, performs the exchange, and writes curl-shaped output.
// env and workDir are per-invocation; nothing here mutates the shared Command.
func (c *Command) do(ctx context.Context, req *Request, env map[string]string, workDir string, stdout, stderr io.Writer) error {
	proxyURL, caPath := c.egress(env)

	client, err := c.buildClient(proxyURL, caPath, req)
	if err != nil {
		return err
	}

	target, err := url.Parse(strings.TrimSpace(req.URL))
	if err != nil || target.Scheme == "" || target.Host == "" {
		return fmt.Errorf("curl: (3) URL rejected: %s", req.URL)
	}

	body, contentType, err := buildBody(req, target, workDir)
	if err != nil {
		return err
	}

	if req.MaxTime > 0 {
		var cancel context.CancelFunc
		ctx, cancel = context.WithTimeout(ctx, req.MaxTime)
		defer cancel()
	}

	httpReq, err := http.NewRequestWithContext(ctx, req.Method, target.String(), body)
	if err != nil {
		return fmt.Errorf("curl: %w", err)
	}
	applyHeaders(httpReq, req, contentType)

	if req.CookieIn != "" {
		if err := seedCookies(client.Jar, target, req.CookieIn, workDir); err != nil {
			return err
		}
	}

	if req.Verbose && !req.Silent {
		writeVerboseRequest(stderr, httpReq)
	}

	resp, err := client.Do(httpReq)
	if err != nil {
		return fmt.Errorf("curl: (7) %w", err)
	}
	defer resp.Body.Close()

	if req.Verbose && !req.Silent {
		writeVerboseResponse(stderr, resp)
	}

	out, closeOut, err := outputWriter(req, workDir, stdout)
	if err != nil {
		return err
	}
	defer closeOut()

	if req.Include {
		writeStatusAndHeaders(out, resp)
	}
	written, err := io.Copy(out, resp.Body)
	if err != nil {
		return fmt.Errorf("curl: (56) %w", err)
	}

	if req.CookieJar != "" {
		if err := writeCookieJar(client.Jar, resp.Request.URL, resolvePath(workDir, req.CookieJar)); err != nil && !req.Silent {
			c.Logger.Warnf("curl: write cookie jar: %s", err)
		}
	}

	if req.WriteOut != "" {
		fmt.Fprint(stdout, expandWriteOut(req.WriteOut, resp, written))
	}

	c.emitArtifact(ctx, resp, written)
	return nil
}

// egress reads the hub proxy and CA path the runner injected into this
// execution's environment. The proxy URL already carries the tool-call id as
// its username, so captured flows attribute to this call; the CA is present
// only while the hub is intercepting. Falls back to the static scanner proxy.
func (c *Command) egress(env map[string]string) (proxyURL, caPath string) {
	for _, key := range []string{"ALL_PROXY", "all_proxy", "HTTPS_PROXY", "https_proxy", "HTTP_PROXY", "http_proxy"} {
		if v := env[key]; v != "" {
			proxyURL = v
			break
		}
	}
	for _, key := range []string{"CURL_CA_BUNDLE", "SSL_CERT_FILE"} {
		if v := env[key]; v != "" {
			caPath = v
			break
		}
	}
	if proxyURL == "" {
		proxyURL = c.Proxy
	}
	return proxyURL, caPath
}

func (c *Command) buildClient(proxyURL, caPath string, req *Request) (*http.Client, error) {
	tlsConfig := &tls.Config{MinVersion: tls.VersionTLS12}
	switch {
	case req.Insecure:
		tlsConfig.InsecureSkipVerify = true
	case caPath != "":
		pool, err := caPool(caPath)
		if err != nil {
			return nil, err
		}
		tlsConfig.RootCAs = pool
	}

	dialTimeout := req.ConnectTimeout
	if dialTimeout == 0 {
		dialTimeout = 30 * time.Second
	}
	transport := &http.Transport{
		TLSClientConfig:     tlsConfig,
		DialContext:         (&net.Dialer{Timeout: dialTimeout}).DialContext,
		ForceAttemptHTTP2:   true,
		MaxIdleConns:        100,
		IdleConnTimeout:     90 * time.Second,
		TLSHandshakeTimeout: dialTimeout,
	}
	if proxyURL != "" {
		parsed, err := url.Parse(proxyURL)
		if err != nil {
			return nil, fmt.Errorf("curl: invalid proxy %q: %w", proxyURL, err)
		}
		transport.Proxy = http.ProxyURL(parsed)
	}

	jar, _ := cookiejar.New(nil)
	maxRedirs := req.MaxRedirs
	client := &http.Client{
		Transport: transport,
		Jar:       jar,
		CheckRedirect: func(r *http.Request, via []*http.Request) error {
			if !req.Follow {
				return http.ErrUseLastResponse
			}
			if maxRedirs >= 0 && len(via) >= maxRedirs {
				return fmt.Errorf("curl: (47) Maximum (%d) redirects followed", maxRedirs)
			}
			return nil
		},
	}
	return client, nil
}

// caPool builds a root pool seeded from the system pool plus the hub CA, so the
// tool trusts intercepted HTTPS without losing trust in the rest of the world.
func caPool(caPath string) (*x509.CertPool, error) {
	pem, err := os.ReadFile(caPath)
	if err != nil {
		return nil, fmt.Errorf("curl: read CA bundle: %w", err)
	}
	pool, err := x509.SystemCertPool()
	if err != nil || pool == nil {
		pool = x509.NewCertPool()
	}
	if !pool.AppendCertsFromPEM(pem) {
		return nil, fmt.Errorf("curl: CA bundle %q contained no certificates", caPath)
	}
	return pool, nil
}

// buildBody assembles the request body from -d parts. With -G the data is folded
// into the URL query and no body is sent.
func buildBody(req *Request, target *url.URL, workDir string) (io.Reader, string, error) {
	if len(req.Data) == 0 {
		return nil, "", nil
	}
	segments := make([]string, 0, len(req.Data))
	for _, part := range req.Data {
		value := part.Value
		if part.File {
			raw, err := os.ReadFile(resolvePath(workDir, part.Value))
			if err != nil {
				return nil, "", fmt.Errorf("curl: (26) Failed to open %q: %w", part.Value, err)
			}
			value = string(raw)
			if !part.Binary {
				// Non-binary -d strips line breaks from file content, like curl.
				value = strings.NewReplacer("\r", "", "\n", "").Replace(value)
			}
		}
		segments = append(segments, value)
	}
	joined := strings.Join(segments, "&")

	if req.Get {
		// curl appends -d data to the query verbatim (no re-encoding); that is
		// --data-urlencode's job, which is deferred.
		if target.RawQuery == "" {
			target.RawQuery = joined
		} else {
			target.RawQuery += "&" + joined
		}
		return nil, "", nil
	}
	return strings.NewReader(joined), "application/x-www-form-urlencoded", nil
}

func applyHeaders(httpReq *http.Request, req *Request, contentType string) {
	set := make(map[string]bool)
	for _, h := range req.Headers {
		canonical := http.CanonicalHeaderKey(h.Name)
		set[canonical] = true
		switch {
		case h.Remove:
			httpReq.Header.Del(canonical)
		case canonical == "Host":
			httpReq.Host = h.Value
		default:
			httpReq.Header.Add(canonical, h.Value)
		}
	}

	if req.UserAgent != "" {
		httpReq.Header.Set("User-Agent", req.UserAgent)
		set["User-Agent"] = true
	}
	if req.Referer != "" {
		httpReq.Header.Set("Referer", req.Referer)
		set["Referer"] = true
	}
	if req.User != "" {
		token := base64.StdEncoding.EncodeToString([]byte(req.User))
		httpReq.Header.Set("Authorization", "Basic "+token)
		set["Authorization"] = true
	}
	if contentType != "" && !set["Content-Type"] {
		httpReq.Header.Set("Content-Type", contentType)
		set["Content-Type"] = true
	}

	// Fill browser defaults only where the caller was silent.
	if !set["User-Agent"] {
		httpReq.Header.Set("User-Agent", chromeUserAgent)
	}
	for _, d := range browserDefaults {
		if !set[http.CanonicalHeaderKey(d.Name)] {
			httpReq.Header.Set(d.Name, d.Value)
		}
	}
}

func outputWriter(req *Request, workDir string, stdout io.Writer) (io.Writer, func(), error) {
	if req.Output == "" || req.Output == "-" {
		return stdout, func() {}, nil
	}
	path := resolvePath(workDir, req.Output)
	file, err := os.Create(path)
	if err != nil {
		return nil, nil, fmt.Errorf("curl: (23) Failed to create %q: %w", req.Output, err)
	}
	return file, func() { _ = file.Close() }, nil
}

func (c *Command) emitArtifact(ctx context.Context, resp *http.Response, size int64) {
	if c.Events == nil || resp.Request == nil {
		return
	}
	summary := struct {
		URL         string `json:"url"`
		Status      int    `json:"status"`
		ContentType string `json:"content_type,omitempty"`
		Size        int64  `json:"size"`
	}{
		URL:         resp.Request.URL.String(),
		Status:      resp.StatusCode,
		ContentType: resp.Header.Get("Content-Type"),
		Size:        size,
	}
	c.EmitArtifactCtx(ctx, "curl", toolpb.ArtifactKindWeb, summary.URL, summary)
}

// resolvePath anchors a relative file path at the tool's working directory,
// matching how the other scanners treat file arguments.
func resolvePath(workDir, path string) string {
	if path == "" || filepath.IsAbs(path) || workDir == "" {
		return path
	}
	return filepath.Join(workDir, path)
}

func writeStatusAndHeaders(w io.Writer, resp *http.Response) {
	fmt.Fprintf(w, "%s %s\r\n", resp.Proto, resp.Status)
	_ = resp.Header.Write(w)
	fmt.Fprint(w, "\r\n")
}

func writeVerboseRequest(w io.Writer, req *http.Request) {
	fmt.Fprintf(w, "> %s %s %s\r\n", req.Method, req.URL.RequestURI(), req.Proto)
	fmt.Fprintf(w, "> Host: %s\r\n", req.Host)
	for name, values := range req.Header {
		for _, v := range values {
			fmt.Fprintf(w, "> %s: %s\r\n", name, v)
		}
	}
	fmt.Fprint(w, ">\r\n")
}

func writeVerboseResponse(w io.Writer, resp *http.Response) {
	fmt.Fprintf(w, "< %s %s\r\n", resp.Proto, resp.Status)
	for name, values := range resp.Header {
		for _, v := range values {
			fmt.Fprintf(w, "< %s: %s\r\n", name, v)
		}
	}
	fmt.Fprint(w, "<\r\n")
}

// expandWriteOut supports the curl -w variables the agent uses most.
func expandWriteOut(format string, resp *http.Response, size int64) string {
	replacer := strings.NewReplacer(
		"%{http_code}", strconv.Itoa(resp.StatusCode),
		"%{response_code}", strconv.Itoa(resp.StatusCode),
		"%{url_effective}", resp.Request.URL.String(),
		"%{content_type}", resp.Header.Get("Content-Type"),
		"%{size_download}", strconv.FormatInt(size, 10),
		"\\n", "\n", "\\t", "\t", "\\r", "\r",
	)
	return replacer.Replace(format)
}
