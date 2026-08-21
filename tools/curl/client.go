package curl

import (
	"bytes"
	"context"
	"crypto/tls"
	"crypto/x509"
	"encoding/base64"
	"errors"
	"fmt"
	"io"
	"mime"
	"mime/multipart"
	"net"
	"net/http"
	"net/http/cookiejar"
	"net/textproto"
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
	if req.Version {
		_, err := fmt.Fprintln(stdout, compatibilityVersion)
		return err
	}

	proxyURL, caPath := c.egress(env)
	if req.Proxy != "" {
		// -x overrides the injected hub egress for this invocation only.
		proxyURL = req.Proxy
		if !strings.Contains(proxyURL, "://") {
			proxyURL = "http://" + proxyURL
		}
	}
	if len(req.Resolve) > 0 && proxyURL != "" {
		// A standard library HTTP proxy owns the destination dial and therefore
		// cannot safely honor a local host mapping without also changing CONNECT
		// and TLS-SNI behavior. Fail explicitly instead of silently ignoring the
		// option (or bypassing the evidence proxy).
		return fmt.Errorf("curl: --resolve cannot be used with a proxy")
	}

	client, err := c.buildClient(proxyURL, caPath, req)
	if err != nil {
		return err
	}
	responses := &responseCapture{}
	client.Transport = &capturingTransport{base: client.Transport, capture: responses}

	target, err := url.Parse(strings.TrimSpace(req.URL))
	if err != nil || target.Scheme == "" || target.Host == "" {
		return fmt.Errorf("curl: (3) URL rejected: %s", req.URL)
	}
	if !req.PathAsIs {
		normalizeCurlURLPath(target)
	}

	body, contentType, err := buildBody(req, target, workDir)
	if err != nil {
		return err
	}
	if req.Head {
		// --head suppresses the response body even when -X explicitly selects a
		// different method (curl uses this combination for header-only probes).
		// A HEAD transfer also never carries a request body.
		body = nil
		contentType = ""
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
		if isTimeoutError(err) {
			return fmt.Errorf("curl: (28) %w", err)
		}
		return fmt.Errorf("curl: (7) %w", err)
	}
	defer resp.Body.Close()

	if req.Verbose && !req.Silent {
		writeVerboseResponse(stderr, resp)
	}

	if err := dumpResponseHeaders(req, responses.all(), workDir, stdout); err != nil {
		return err
	}
	failed := req.Fail && resp.StatusCode >= http.StatusBadRequest
	if failed && !req.Include {
		// curl leaves -o untouched (and does not create it) when --fail rejects
		// a response, unless headers were explicitly requested with -i.
		return c.failResponse(ctx, client, req, resp, workDir, stdout)
	}

	out, closeOut, err := outputWriter(req, workDir, stdout)
	if err != nil {
		return err
	}
	defer closeOut()

	if req.Include {
		for _, response := range responses.all() {
			writeStatusAndHeaders(out, response)
		}
	}
	if failed {
		return c.failResponse(ctx, client, req, resp, workDir, stdout)
	}
	var written int64
	if req.Head {
		// Drain and discard a body some non-conforming servers attach to a
		// header-only request, so the connection remains reusable.
		written, err = io.Copy(io.Discard, resp.Body)
		written = 0
	} else {
		written, err = copyResponse(out, resp.Body, req.NoBuffer)
	}
	if err != nil {
		if isTimeoutError(err) {
			return fmt.Errorf("curl: (28) %w", err)
		}
		return fmt.Errorf("curl: (56) %w", err)
	}

	if err := persistResponseCookies(c, client, req, resp, workDir); err != nil {
		return err
	}

	if req.WriteOut != "" {
		fmt.Fprint(stdout, expandWriteOut(req.WriteOut, resp, written))
	}

	c.emitArtifact(ctx, resp, written)
	return nil
}

func (c *Command) failResponse(ctx context.Context, client *http.Client, req *Request, resp *http.Response, workDir string, stdout io.Writer) error {
	if err := persistResponseCookies(c, client, req, resp, workDir); err != nil {
		return err
	}
	if req.WriteOut != "" {
		fmt.Fprint(stdout, expandWriteOut(req.WriteOut, resp, 0))
	}
	c.emitArtifact(ctx, resp, 0)
	return fmt.Errorf("curl: (22) The requested URL returned error: %s", resp.Status)
}

func isTimeoutError(err error) bool {
	if errors.Is(err, context.DeadlineExceeded) {
		return true
	}
	var netErr net.Error
	return errors.As(err, &netErr) && netErr.Timeout()
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
	dialer := &net.Dialer{Timeout: dialTimeout}
	dialContext := dialer.DialContext
	if len(req.Resolve) > 0 {
		resolve := makeResolveMap(req.Resolve)
		dialContext = func(ctx context.Context, network, address string) (net.Conn, error) {
			host, port, err := net.SplitHostPort(address)
			if err == nil {
				entry, ok := lookupResolve(resolve, host, port)
				if ok {
					var lastErr error
					for _, mapped := range entry.Addresses {
						conn, dialErr := dialer.DialContext(ctx, network, net.JoinHostPort(mapped, port))
						if dialErr == nil {
							return conn, nil
						}
						lastErr = dialErr
					}
					if lastErr != nil {
						return nil, lastErr
					}
				}
			}
			return dialer.DialContext(ctx, network, address)
		}
	}
	forceHTTP2 := req.HTTP2 || !req.HTTP11
	transport := &http.Transport{
		TLSClientConfig:     tlsConfig,
		DialContext:         dialContext,
		ForceAttemptHTTP2:   forceHTTP2,
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

// buildBody assembles the request body from -d parts or -F parts. With -G the
// data is folded into the URL query and no body is sent. Parse already rejects
// combining -d with -F and -G with -F.
func buildBody(req *Request, target *url.URL, workDir string) (io.Reader, string, error) {
	if len(req.Form) > 0 {
		return buildForm(req.Form, workDir)
	}
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
			if !part.Binary && !part.URLEncode {
				// Non-binary -d strips line breaks from file content, like curl.
				value = strings.NewReplacer("\r", "", "\n", "").Replace(value)
			}
		}
		if part.URLEncode {
			var err error
			value, err = encodeDataValue(value, part.File, workDir)
			if err != nil {
				return nil, "", err
			}
		}
		segments = append(segments, value)
	}
	joined := strings.Join(segments, "&")

	if req.Get {
		// curl appends -d data to the query verbatim (no re-encoding); encoding
		// is --data-urlencode's job, applied above.
		if target.RawQuery == "" {
			target.RawQuery = joined
		} else {
			target.RawQuery += "&" + joined
		}
		return nil, "", nil
	}
	return strings.NewReader(joined), "application/x-www-form-urlencoded", nil
}

// encodeDataValue applies --data-urlencode semantics to one part: the name
// before the first '=' stays verbatim while the content is percent-encoded;
// name@file (unreachable when File is already set) reads and encodes a file.
func encodeDataValue(value string, fromFile bool, workDir string) (string, error) {
	if fromFile {
		return pctEncode(value), nil
	}
	if name, content, ok := strings.Cut(value, "="); ok {
		return name + "=" + pctEncode(content), nil
	}
	if name, path, ok := strings.Cut(value, "@"); ok && name != "" {
		raw, err := os.ReadFile(resolvePath(workDir, path))
		if err != nil {
			return "", fmt.Errorf("curl: (26) Failed to open %q: %w", path, err)
		}
		return name + "=" + pctEncode(string(raw)), nil
	}
	return pctEncode(value), nil
}

// pctEncode percent-encodes every byte outside the RFC 3986 unreserved set,
// matching curl's --data-urlencode (space becomes %20, not +).
func pctEncode(s string) string {
	const upperhex = "0123456789ABCDEF"
	var sb strings.Builder
	sb.Grow(len(s))
	for i := 0; i < len(s); i++ {
		c := s[i]
		switch {
		case 'A' <= c && c <= 'Z', 'a' <= c && c <= 'z', '0' <= c && c <= '9',
			c == '-', c == '_', c == '.', c == '~':
			sb.WriteByte(c)
		default:
			sb.WriteByte('%')
			sb.WriteByte(upperhex[c>>4])
			sb.WriteByte(upperhex[c&0xF])
		}
	}
	return sb.String()
}

// buildForm assembles a multipart/form-data body from -F parts.
func buildForm(parts []FormPart, workDir string) (io.Reader, string, error) {
	var buf bytes.Buffer
	w := multipart.NewWriter(&buf)
	for _, part := range parts {
		switch {
		case part.File:
			data, err := os.ReadFile(resolvePath(workDir, part.Value))
			if err != nil {
				return nil, "", fmt.Errorf("curl: (26) Failed to open %q: %w", part.Value, err)
			}
			header := make(textproto.MIMEHeader)
			header.Set("Content-Disposition", fmt.Sprintf(`form-data; name="%s"; filename="%s"`,
				formQuoteEscape.Replace(part.Name), formQuoteEscape.Replace(filepath.Base(part.Value))))
			ct := part.Type
			if ct == "" {
				ct = mime.TypeByExtension(filepath.Ext(part.Value))
			}
			if ct == "" {
				ct = "application/octet-stream"
			}
			header.Set("Content-Type", ct)
			pw, err := w.CreatePart(header)
			if err != nil {
				return nil, "", fmt.Errorf("curl: form part %q: %w", part.Name, err)
			}
			if _, err := pw.Write(data); err != nil {
				return nil, "", fmt.Errorf("curl: form part %q: %w", part.Name, err)
			}
		case part.Content:
			data, err := os.ReadFile(resolvePath(workDir, part.Value))
			if err != nil {
				return nil, "", fmt.Errorf("curl: (26) Failed to open %q: %w", part.Value, err)
			}
			// name=<file keeps the text but drops line breaks, like curl.
			value := strings.NewReplacer("\r", "", "\n", "").Replace(string(data))
			if err := w.WriteField(part.Name, value); err != nil {
				return nil, "", fmt.Errorf("curl: form part %q: %w", part.Name, err)
			}
		default:
			if err := w.WriteField(part.Name, part.Value); err != nil {
				return nil, "", fmt.Errorf("curl: form part %q: %w", part.Name, err)
			}
		}
	}
	if err := w.Close(); err != nil {
		return nil, "", fmt.Errorf("curl: close multipart body: %w", err)
	}
	return &buf, w.FormDataContentType(), nil
}

var formQuoteEscape = strings.NewReplacer("\\", "\\\\", `"`, "\\\"")

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

type responseCapture struct {
	responses []*http.Response
}

func (c *responseCapture) add(resp *http.Response) {
	if resp != nil {
		c.responses = append(c.responses, resp)
	}
}

func (c *responseCapture) all() []*http.Response {
	if len(c.responses) == 0 {
		return nil
	}
	return append([]*http.Response(nil), c.responses...)
}

type capturingTransport struct {
	base    http.RoundTripper
	capture *responseCapture
}

func (t *capturingTransport) RoundTrip(req *http.Request) (*http.Response, error) {
	resp, err := t.base.RoundTrip(req)
	if err == nil && t.capture != nil {
		t.capture.add(resp)
	}
	return resp, err
}

func dumpResponseHeaders(req *Request, responses []*http.Response, workDir string, stdout io.Writer) error {
	if req.DumpHeader == "" {
		return nil
	}
	if req.DumpHeader == "-" {
		for _, resp := range responses {
			writeStatusAndHeaders(stdout, resp)
		}
		return nil
	}
	path := resolvePath(workDir, req.DumpHeader)
	file, err := os.Create(path)
	if err != nil {
		return fmt.Errorf("curl: (23) Failed to create dump-header %q: %w", req.DumpHeader, err)
	}
	for _, resp := range responses {
		writeStatusAndHeaders(file, resp)
	}
	if err := file.Close(); err != nil {
		return fmt.Errorf("curl: (23) Failed to close dump-header %q: %w", req.DumpHeader, err)
	}
	return nil
}

func persistResponseCookies(c *Command, client *http.Client, req *Request, resp *http.Response, workDir string) error {
	if req.CookieJar == "" {
		return nil
	}
	if resp == nil || resp.Request == nil {
		return nil
	}
	if err := writeCookieJar(client.Jar, resp.Request.URL, resolvePath(workDir, req.CookieJar)); err != nil && !req.Silent {
		c.Logger.Warnf("curl: write cookie jar: %s", err)
	}
	return nil
}

// copyResponse keeps the normal io.Copy fast path while making -N meaningful
// for writers that expose Flush. net/http response bodies are already streamed;
// this loop only adds an explicit flush after each chunk when requested.
func copyResponse(dst io.Writer, src io.Reader, noBuffer bool) (int64, error) {
	if !noBuffer {
		return io.Copy(dst, src)
	}
	buf := make([]byte, 32*1024)
	var written int64
	for {
		n, readErr := src.Read(buf)
		if n > 0 {
			m, writeErr := dst.Write(buf[:n])
			written += int64(m)
			if writeErr != nil {
				return written, writeErr
			}
			if m != n {
				return written, io.ErrShortWrite
			}
			flushWriter(dst)
		}
		if readErr != nil {
			if readErr == io.EOF {
				return written, nil
			}
			return written, readErr
		}
	}
}

type flusher interface{ Flush() }

func flushWriter(w io.Writer) {
	if f, ok := w.(flusher); ok {
		f.Flush()
	}
}

func makeResolveMap(entries []ResolveEntry) map[string]ResolveEntry {
	resolved := make(map[string]ResolveEntry, len(entries))
	for _, entry := range entries {
		key := resolveKey(entry.Host, entry.Port)
		// The last --resolve entry for a host/port wins, matching curl's
		// resolver table. A leading '+' only changes the DNS-cache lifetime;
		// this client resolves per invocation, so Temporary is informational.
		resolved[key] = entry
	}
	return resolved
}

func lookupResolve(entries map[string]ResolveEntry, host, port string) (ResolveEntry, bool) {
	for _, key := range []string{
		resolveKey(host, port),
		resolveKey("*", port),
		resolveKey(host, "*"),
		resolveKey("*", "*"),
	} {
		if entry, ok := entries[key]; ok {
			return entry, true
		}
	}
	return ResolveEntry{}, false
}

func resolveKey(host, port string) string {
	host = strings.TrimSuffix(strings.ToLower(strings.TrimSpace(host)), ".")
	return host + ":" + strings.TrimSpace(port)
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

// normalizeCurlURLPath implements the URL dot-segment cleanup performed by
// curl unless --path-as-is is selected. It deliberately operates on the raw
// escaped path, so an encoded "%2e" is data rather than a dot segment. Empty
// segments are retained except when consumed by a preceding "..", matching
// curl's treatment of repeated slashes.
func normalizeCurlURLPath(u *url.URL) {
	raw := u.EscapedPath()
	if raw == "" {
		u.Path = "/"
		u.RawPath = ""
		return
	}
	trailingSlash := strings.HasSuffix(raw, "/") || strings.HasSuffix(raw, "/.") || strings.HasSuffix(raw, "/..")
	parts := strings.Split(raw, "/")
	out := make([]string, 0, len(parts))
	for i, part := range parts {
		if i == 0 {
			// Absolute HTTP URLs always have a leading slash. Keep an empty
			// first segment so joining below preserves that invariant.
			out = append(out, part)
			continue
		}
		switch part {
		case ".":
			// A terminal dot segment implies a trailing slash.
			if i == len(parts)-1 {
				trailingSlash = true
			}
		case "..":
			if i == len(parts)-1 {
				trailingSlash = true
			}
			if len(out) > 1 {
				// Pop one segment, including an empty segment. This is why
				// /a//../b becomes /a/b rather than /b.
				out = out[:len(out)-1]
			}
		case "":
			// curl collapses duplicate slashes at the beginning of an HTTP
			// path, while preserving empty segments in the middle.
			if len(out) == 1 {
				continue
			}
			out = append(out, part)
		default:
			out = append(out, part)
		}
	}
	cleaned := strings.Join(out, "/")
	if cleaned == "" {
		cleaned = "/"
	}
	if trailingSlash && cleaned != "/" && !strings.HasSuffix(cleaned, "/") {
		cleaned += "/"
	}
	decoded, err := url.PathUnescape(cleaned)
	if err != nil {
		// Keep the original URL if it contains malformed escaping; the request
		// constructor will return curl's normal URL error downstream.
		return
	}
	u.Path = decoded
	if decoded == cleaned {
		u.RawPath = ""
	} else {
		u.RawPath = cleaned
	}
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
