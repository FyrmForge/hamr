package devserver

import (
	"bytes"
	_ "embed"
	"io"
	"net"
	"net/http"
	"net/http/httputil"
	"net/url"
	"strconv"
	"strings"
)

//go:embed reload.js
var reloadJS []byte

var reloadScript = []byte("\n<script>\n" + string(reloadJS) + "\n</script>\n")

func init() {
	// Rebuild reloadScript after init since string(reloadJS) in var decl
	// doesn't work with embed at package init time — embed is populated
	// before init() runs but the var decl for reloadScript uses the
	// zero-value reloadJS. Rebuild here.
	reloadScript = []byte("\n<script>\n" + string(reloadJS) + "\n</script>\n")
}

// NewProxyHandler creates an HTTP handler that reverse-proxies to the target
// address, optionally injecting the live reload script into HTML responses.
// The SSE broker handler is mounted at /__hamr/reload.
func NewProxyHandler(target string, broker *SSEBroker, injectReload bool) http.Handler {
	targetURL := &url.URL{
		Scheme: "http",
		Host:   normalizeHost(target),
	}

	proxy := httputil.NewSingleHostReverseProxy(targetURL)
	origDirector := proxy.Director
	proxy.Director = func(req *http.Request) {
		origDirector(req)
		if injectReload {
			// Force identity responses so HTML injection can safely mutate bytes.
			req.Header.Del("Accept-Encoding")
		}
	}
	proxy.Transport = &http.Transport{
		// Disable compression so upstream sends uncompressed HTML,
		// making body injection straightforward.
		DisableCompression: true,
	}

	if injectReload {
		proxy.ModifyResponse = injectReloadScript
	}

	mux := http.NewServeMux()
	mux.Handle("/__hamr/reload", broker.Handler())
	mux.Handle("/", proxy)

	return mux
}

func normalizeHost(addr string) string {
	if strings.HasPrefix(addr, ":") {
		return "localhost" + addr
	}
	return addr
}

func injectReloadScript(resp *http.Response) error {
	if resp.Request != nil && resp.Request.Method == http.MethodHead {
		return nil
	}
	switch resp.StatusCode {
	case http.StatusNoContent, http.StatusNotModified:
		return nil
	}
	if resp.StatusCode >= 100 && resp.StatusCode < 200 {
		return nil
	}

	ct := strings.ToLower(resp.Header.Get("Content-Type"))
	if !strings.Contains(ct, "text/html") {
		return nil
	}

	encoding := strings.ToLower(strings.TrimSpace(resp.Header.Get("Content-Encoding")))
	if encoding != "" && encoding != "identity" {
		return nil
	}

	body, err := io.ReadAll(resp.Body)
	resp.Body.Close()
	if err != nil {
		return err
	}

	// Find </body> and inject script before it.
	lower := bytes.ToLower(body)
	idx := bytes.LastIndex(lower, []byte("</body>"))
	if idx >= 0 {
		var buf bytes.Buffer
		buf.Grow(len(body) + len(reloadScript))
		buf.Write(body[:idx])
		buf.Write(reloadScript)
		buf.Write(body[idx:])
		body = buf.Bytes()
	} else {
		// No </body> found — append script at the end.
		body = append(body, reloadScript...)
	}

	resp.Body = io.NopCloser(bytes.NewReader(body))
	resp.ContentLength = int64(len(body))
	resp.Header.Set("Content-Length", strconv.Itoa(len(body)))
	// Remove Content-Encoding since we've modified the body.
	resp.Header.Del("Content-Encoding")

	return nil
}

// ListenAndServeProxy starts the proxy server. It blocks until the context
// is cancelled or an error occurs.
func ListenAndServeProxy(addr string, handler http.Handler) (*http.Server, net.Listener, error) {
	ln, err := net.Listen("tcp", addr)
	if err != nil {
		return nil, nil, err
	}

	srv := &http.Server{Handler: handler}
	go srv.Serve(ln)

	return srv, ln, nil
}
