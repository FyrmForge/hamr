package devserver

import (
	"bytes"
	"context"
	_ "embed"
	"encoding/json"
	"io"
	"net"
	"net/http"
	"net/http/httputil"
	"net/url"
	"strconv"
	"strings"
	"time"
)

//go:embed reload.js
var reloadJS []byte

//go:embed logo.png
var logoPNG []byte

var reloadScript []byte

func init() {
	reloadScript = []byte("\n<script>\n" + string(reloadJS) + "\n</script>\n")
}

// NewProxyHandler creates an HTTP handler that reverse-proxies to the target
// address, optionally injecting the live reload script into HTML responses.
// The SSE broker handler is mounted at /__hamr/reload.
// If errorState is non-nil, HTML requests are intercepted with an error page
// when there are active build errors.
// If mailMock is non-nil, the mail inbox UI and ingest endpoint are mounted
// under /__hamr/mail.
func NewProxyHandler(target string, broker *SSEBroker, errorState *ErrorState, logBuf *LogBuffer, actions *DevActions, mailMock *MailMock, stripeMock *StripeMock, injectReload bool) http.Handler {
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
	dialer := &net.Dialer{Timeout: 2 * time.Second, KeepAlive: 30 * time.Second}
	proxy.Transport = &http.Transport{
		// Disable compression so upstream sends uncompressed HTML,
		// making body injection straightforward.
		DisableCompression: true,
		// Short timeouts so a half-up backend (port open, not serving) fails
		// fast into ErrorHandler instead of stalling browser requests.
		DialContext: func(ctx context.Context, network, addr string) (net.Conn, error) {
			return dialer.DialContext(ctx, network, addr)
		},
		ResponseHeaderTimeout: 2 * time.Second,
		IdleConnTimeout:       30 * time.Second,
		MaxIdleConns:          100,
	}

	if injectReload {
		proxy.ModifyResponse = injectReloadScript
	}
	// Flush immediately so streaming responses (SSE, chunked HTMX) aren't
	// buffered end-to-end by the proxy.
	proxy.FlushInterval = -1

	// Handle transport errors (backend down / connection refused).
	waitingPage := renderWaitingPage(target)
	proxy.ErrorHandler = func(w http.ResponseWriter, r *http.Request, err error) {
		if acceptsHTML(r) {
			w.Header().Set("Content-Type", "text/html; charset=utf-8")
			w.Header().Set("X-Hamr-Waiting", "1")
			w.WriteHeader(http.StatusBadGateway)
			w.Write(waitingPage) //nolint:errcheck
			return
		}
		w.Header().Set("Content-Type", "application/json")
		w.Header().Set("X-Hamr-Waiting", "1")
		w.WriteHeader(http.StatusBadGateway)
		w.Write([]byte(`{"error":"backend unavailable"}`)) //nolint:errcheck
	}

	var rootHandler http.Handler = proxy
	if errorState != nil {
		rootHandler = &errorInterceptor{errorState: errorState, next: proxy}
	}

	mux := http.NewServeMux()
	mux.Handle("/__hamr/reload", broker.Handler())
	mux.HandleFunc("/__hamr/logo.png", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "image/png")
		w.Header().Set("Cache-Control", "public, max-age=86400")
		w.Write(logoPNG) //nolint:errcheck
	})
	if actions != nil {
		actions.RegisterRoutes(mux)
	}
	if mailMock != nil {
		mailMock.RegisterRoutes(mux)
	}
	if stripeMock != nil {
		stripeMock.RegisterAPIRoutes(mux)
		stripeMock.RegisterUIRoutes(mux)
	}
	if logBuf != nil {
		mux.HandleFunc("/__hamr/logs", func(w http.ResponseWriter, r *http.Request) {
			w.Header().Set("Content-Type", "application/json")
			json.NewEncoder(w).Encode(logBuf.Lines()) //nolint:errcheck
		})
	}
	mux.Handle("/", rootHandler)

	return mux
}

// errorInterceptor serves a build error page for HTML requests when there are
// active build errors. Non-HTML and HTMX requests pass through to the backend.
type errorInterceptor struct {
	errorState *ErrorState
	next       http.Handler
}

func (e *errorInterceptor) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	if e.errorState.HasErrors() && acceptsHTML(r) {
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		w.WriteHeader(http.StatusBadGateway)
		w.Write(renderErrorPage(e.errorState.Snapshot())) //nolint:errcheck
		return
	}
	e.next.ServeHTTP(w, r)
}

// acceptsHTML returns true if the request is a browser navigation requesting
// HTML (not an HTMX partial or API call).
func acceptsHTML(r *http.Request) bool {
	if r.Header.Get("HX-Request") != "" {
		return false
	}
	return strings.Contains(r.Header.Get("Accept"), "text/html")
}

// normalizeHost rewrites a listen/target address into a client-reachable
// host:port. The raw config values (":3000", "0.0.0.0:3000", "[::]:3000")
// are valid bind addresses but not valid client destinations — they mean
// "listen on every interface." A browser or a spawned app process needs an
// actual reachable host, so we substitute "localhost" for the unspecified
// forms. Loopback addresses and hostnames are returned untouched.
func normalizeHost(addr string) string {
	if strings.HasPrefix(addr, ":") {
		return "localhost" + addr
	}
	host, port, err := net.SplitHostPort(addr)
	if err != nil {
		return addr
	}
	switch host {
	case "0.0.0.0", "::", "[::]":
		return "localhost:" + port
	}
	return addr
}

// maxInjectBody caps how large a response we're willing to buffer in-memory
// for reload-script injection. Responses above this pass through untouched
// so slow or large streams don't stall the proxy.
const maxInjectBody = 5 * 1024 * 1024

func injectReloadScript(resp *http.Response) error {
	if resp.Request != nil && resp.Request.Method == http.MethodHead {
		return nil
	}
	// HTMX partials don't need (and shouldn't contain) the reload script —
	// injection would also force full-body buffering, stalling slow handlers.
	if resp.Request != nil && resp.Request.Header.Get("HX-Request") != "" {
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

	// Skip injection when the upstream advertises a response larger than we're
	// willing to buffer. Chunked responses (Content-Length -1) ARE buffered up
	// to maxInjectBody — the LimitReader + post-read size check below guard
	// against runaway payloads. Buffering localhost dev responses here is the
	// price for getting the reload script into templ's chunked output.
	if resp.ContentLength > maxInjectBody {
		return nil
	}

	body, err := io.ReadAll(io.LimitReader(resp.Body, maxInjectBody+1))
	_ = resp.Body.Close()
	if err != nil {
		return err
	}
	if int64(len(body)) > maxInjectBody {
		// Larger than advertised — send original bytes through without injection.
		resp.Body = io.NopCloser(bytes.NewReader(body))
		return nil
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
// is cancelled or an error occurs. Kept for tests and external callers
// that don't need the +1-on-busy port walking — the dev runner uses
// listenWalk + serveProxy directly so it can react to EADDRINUSE before
// constructing the handler.
func ListenAndServeProxy(addr string, handler http.Handler) (*http.Server, net.Listener, error) {
	ln, err := net.Listen("tcp", addr)
	if err != nil {
		return nil, nil, err
	}
	return serveProxy(ln, handler), ln, nil
}

// serveProxy attaches handler to a fresh *http.Server and serves on the
// already-bound listener in a goroutine. Returns the server so the caller
// can Close it on shutdown. The listener's lifetime is owned by the
// caller; Close on the returned server will close the listener too.
func serveProxy(ln net.Listener, handler http.Handler) *http.Server {
	srv := &http.Server{
		Handler: handler,
		// Slowloris guard without capping body or long-lived (SSE) writes.
		ReadHeaderTimeout: 10 * time.Second,
		IdleTimeout:       120 * time.Second,
	}
	go func() { _ = srv.Serve(ln) }()
	return srv
}
