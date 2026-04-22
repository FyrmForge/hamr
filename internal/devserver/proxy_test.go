package devserver

import (
	"bytes"
	"compress/gzip"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strconv"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestInjectReloadScript_HTML(t *testing.T) {
	backend := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		_, _ = w.Write([]byte("<html><body><h1>Hello</h1></body></html>"))
	}))
	defer backend.Close()

	broker := NewSSEBroker(nil, nil, nil, false)
	handler := NewProxyHandler(backend.Listener.Addr().String(), broker, nil, nil, nil, nil, true)
	proxy := httptest.NewServer(handler)
	defer proxy.Close()

	resp, err := http.Get(proxy.URL)
	require.NoError(t, err)
	defer func() { _ = resp.Body.Close() }()

	body, err := io.ReadAll(resp.Body)
	require.NoError(t, err)

	bodyStr := string(body)
	assert.Contains(t, bodyStr, "<script>")
	assert.Contains(t, bodyStr, "/__hamr/reload")
	assert.Contains(t, bodyStr, "</body>")
	// Script should come before </body>.
	assert.Less(t,
		indexOf(bodyStr, "EventSource"),
		indexOf(bodyStr, "</body>"),
	)
}

func TestInjectReloadScript_ContentLength(t *testing.T) {
	backend := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "text/html")
		_, _ = w.Write([]byte("<html><body>Hello</body></html>"))
	}))
	defer backend.Close()

	broker := NewSSEBroker(nil, nil, nil, false)
	handler := NewProxyHandler(backend.Listener.Addr().String(), broker, nil, nil, nil, nil, true)
	proxy := httptest.NewServer(handler)
	defer proxy.Close()

	resp, err := http.Get(proxy.URL)
	require.NoError(t, err)
	defer func() { _ = resp.Body.Close() }()

	body, err := io.ReadAll(resp.Body)
	require.NoError(t, err)

	// Content-Length header should match actual body length.
	cl := resp.Header.Get("Content-Length")
	if cl != "" {
		clInt, err := strconv.Atoi(cl)
		require.NoError(t, err)
		assert.Equal(t, len(body), clInt)
	}
}

func TestInjectReloadScript_NonHTML(t *testing.T) {
	backend := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"status":"ok"}`))
	}))
	defer backend.Close()

	broker := NewSSEBroker(nil, nil, nil, false)
	handler := NewProxyHandler(backend.Listener.Addr().String(), broker, nil, nil, nil, nil, true)
	proxy := httptest.NewServer(handler)
	defer proxy.Close()

	resp, err := http.Get(proxy.URL)
	require.NoError(t, err)
	defer func() { _ = resp.Body.Close() }()

	body, err := io.ReadAll(resp.Body)
	require.NoError(t, err)

	assert.Equal(t, `{"status":"ok"}`, string(body))
}

func TestInjectReloadScript_NoBodyTag(t *testing.T) {
	backend := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "text/html")
		_, _ = w.Write([]byte("<html><h1>No body tag</h1></html>"))
	}))
	defer backend.Close()

	broker := NewSSEBroker(nil, nil, nil, false)
	handler := NewProxyHandler(backend.Listener.Addr().String(), broker, nil, nil, nil, nil, true)
	proxy := httptest.NewServer(handler)
	defer proxy.Close()

	resp, err := http.Get(proxy.URL)
	require.NoError(t, err)
	defer func() { _ = resp.Body.Close() }()

	body, err := io.ReadAll(resp.Body)
	require.NoError(t, err)

	bodyStr := string(body)
	// Script should still be appended.
	assert.Contains(t, bodyStr, "<script>")
	assert.Contains(t, bodyStr, "EventSource")
}

func TestInjectReloadScript_EmptyBody(t *testing.T) {
	backend := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "text/html")
		w.WriteHeader(http.StatusOK)
		// Empty body.
	}))
	defer backend.Close()

	broker := NewSSEBroker(nil, nil, nil, false)
	handler := NewProxyHandler(backend.Listener.Addr().String(), broker, nil, nil, nil, nil, true)
	proxy := httptest.NewServer(handler)
	defer proxy.Close()

	resp, err := http.Get(proxy.URL)
	require.NoError(t, err)
	defer func() { _ = resp.Body.Close() }()

	body, err := io.ReadAll(resp.Body)
	require.NoError(t, err)

	// Even with empty body, script is appended.
	assert.Contains(t, string(body), "<script>")
}

func TestInjectReloadScript_MultipleBodyTags(t *testing.T) {
	backend := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "text/html")
		// Malformed HTML with two </body> tags.
		_, _ = w.Write([]byte("<html><body>one</body><body>two</body></html>"))
	}))
	defer backend.Close()

	broker := NewSSEBroker(nil, nil, nil, false)
	handler := NewProxyHandler(backend.Listener.Addr().String(), broker, nil, nil, nil, nil, true)
	proxy := httptest.NewServer(handler)
	defer proxy.Close()

	resp, err := http.Get(proxy.URL)
	require.NoError(t, err)
	defer func() { _ = resp.Body.Close() }()

	body, err := io.ReadAll(resp.Body)
	require.NoError(t, err)

	bodyStr := string(body)
	// Script injected before the LAST </body>.
	assert.Contains(t, bodyStr, "<script>")
	// The first </body> should remain intact.
	firstIdx := indexOf(bodyStr, "</body>")
	scriptIdx := indexOf(bodyStr, "<script>")
	assert.Less(t, firstIdx, scriptIdx, "script should be injected after the first </body>")
}

func TestInjectReloadScript_Disabled(t *testing.T) {
	backend := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "text/html")
		_, _ = w.Write([]byte("<html><body>Hello</body></html>"))
	}))
	defer backend.Close()

	broker := NewSSEBroker(nil, nil, nil, false)
	handler := NewProxyHandler(backend.Listener.Addr().String(), broker, nil, nil, nil, nil, false)
	proxy := httptest.NewServer(handler)
	defer proxy.Close()

	resp, err := http.Get(proxy.URL)
	require.NoError(t, err)
	defer func() { _ = resp.Body.Close() }()

	body, err := io.ReadAll(resp.Body)
	require.NoError(t, err)

	assert.NotContains(t, string(body), "<script>")
}

func TestInjectReloadScript_CSSResponse(t *testing.T) {
	backend := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "text/css")
		_, _ = w.Write([]byte("body { color: red; }"))
	}))
	defer backend.Close()

	broker := NewSSEBroker(nil, nil, nil, false)
	handler := NewProxyHandler(backend.Listener.Addr().String(), broker, nil, nil, nil, nil, true)
	proxy := httptest.NewServer(handler)
	defer proxy.Close()

	resp, err := http.Get(proxy.URL)
	require.NoError(t, err)
	defer func() { _ = resp.Body.Close() }()

	body, err := io.ReadAll(resp.Body)
	require.NoError(t, err)

	assert.Equal(t, "body { color: red; }", string(body))
}

func TestInjectReloadScript_EncodedResponseSkipped(t *testing.T) {
	var gz bytes.Buffer
	zw := gzip.NewWriter(&gz)
	_, err := zw.Write([]byte("<html><body>Hello</body></html>"))
	require.NoError(t, err)
	require.NoError(t, zw.Close())

	resp := &http.Response{
		StatusCode: http.StatusOK,
		Header: http.Header{
			"Content-Type":     []string{"text/html"},
			"Content-Encoding": []string{"gzip"},
		},
		Body: io.NopCloser(bytes.NewReader(gz.Bytes())),
		Request: &http.Request{
			Method: http.MethodGet,
		},
	}

	require.NoError(t, injectReloadScript(resp))

	body, err := io.ReadAll(resp.Body)
	require.NoError(t, err)
	assert.Equal(t, gz.Bytes(), body)
	assert.Equal(t, "gzip", resp.Header.Get("Content-Encoding"))
}

func TestNewProxyHandler_StripsAcceptEncodingForInjection(t *testing.T) {
	reqHeader := make(chan http.Header, 1)
	backend := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		reqHeader <- r.Header.Clone()
		w.Header().Set("Content-Type", "text/html")
		_, _ = w.Write([]byte("<html><body>Hello</body></html>"))
	}))
	defer backend.Close()

	broker := NewSSEBroker(nil, nil, nil, false)
	handler := NewProxyHandler(backend.Listener.Addr().String(), broker, nil, nil, nil, nil, true)
	proxy := httptest.NewServer(handler)
	defer proxy.Close()

	req, err := http.NewRequest(http.MethodGet, proxy.URL, nil)
	require.NoError(t, err)
	req.Header.Set("Accept-Encoding", "gzip")
	resp, err := http.DefaultClient.Do(req)
	require.NoError(t, err)
	defer func() { _ = resp.Body.Close() }()

	select {
	case hdr := <-reqHeader:
		assert.Empty(t, hdr.Get("Accept-Encoding"))
	case <-time.After(time.Second):
		t.Fatal("backend did not receive proxied request")
	}
}

func TestSSEEndpoint(t *testing.T) {
	broker := NewSSEBroker(nil, nil, nil, false)
	handler := NewProxyHandler("localhost:9999", broker, nil, nil, nil, nil, true)
	proxy := httptest.NewServer(handler)
	defer proxy.Close()

	resp, err := http.Get(proxy.URL + "/__hamr/reload")
	require.NoError(t, err)
	defer func() { _ = resp.Body.Close() }()

	assert.Equal(t, "text/event-stream", resp.Header.Get("Content-Type"))
}

func TestNormalizeHost(t *testing.T) {
	assert.Equal(t, "localhost:8080", normalizeHost(":8080"))
	assert.Equal(t, "localhost:3000", normalizeHost(":3000"))
	assert.Equal(t, "myhost:8080", normalizeHost("myhost:8080"))
	assert.Equal(t, "127.0.0.1:9090", normalizeHost("127.0.0.1:9090"))
}

func TestListenAndServeProxy(t *testing.T) {
	broker := NewSSEBroker(nil, nil, nil, false)
	handler := NewProxyHandler("localhost:9999", broker, nil, nil, nil, nil, false)

	srv, ln, err := ListenAndServeProxy(":0", handler)
	require.NoError(t, err)
	require.NotNil(t, srv)
	require.NotNil(t, ln)
	defer func() { _ = srv.Close() }()

	// Should be listening.
	addr := ln.Addr().String()
	resp, err := http.Get("http://" + addr + "/__hamr/reload")
	require.NoError(t, err)
	_ = resp.Body.Close()
	assert.Equal(t, "text/event-stream", resp.Header.Get("Content-Type"))
}

func TestListenAndServeProxy_InvalidAddr(t *testing.T) {
	broker := NewSSEBroker(nil, nil, nil, false)
	handler := NewProxyHandler("localhost:9999", broker, nil, nil, nil, nil, false)

	_, _, err := ListenAndServeProxy("invalid-not-an-addr", handler)
	assert.Error(t, err)
}

func TestErrorPage_ServedOnBuildError(t *testing.T) {
	backend := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "text/html")
		_, _ = w.Write([]byte("<html><body>OK</body></html>"))
	}))
	defer backend.Close()

	es := NewErrorState()
	es.Set("go", "cannot find package main")
	broker := NewSSEBroker(nil, nil, nil, false)
	handler := NewProxyHandler(backend.Listener.Addr().String(), broker, es, nil, nil, nil, true)
	proxy := httptest.NewServer(handler)
	defer proxy.Close()

	req, err := http.NewRequest(http.MethodGet, proxy.URL, nil)
	require.NoError(t, err)
	req.Header.Set("Accept", "text/html,application/xhtml+xml")

	resp, err := http.DefaultClient.Do(req)
	require.NoError(t, err)
	defer func() { _ = resp.Body.Close() }()

	assert.Equal(t, http.StatusBadGateway, resp.StatusCode)
	body, err := io.ReadAll(resp.Body)
	require.NoError(t, err)
	bodyStr := string(body)
	assert.Contains(t, bodyStr, "Build Error")
	assert.Contains(t, bodyStr, "cannot find package main")
	assert.Contains(t, bodyStr, "__hamr_error_page")
}

func TestErrorPage_SkippedForAPI(t *testing.T) {
	backend := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"ok":true}`))
	}))
	defer backend.Close()

	es := NewErrorState()
	es.Set("go", "error")
	broker := NewSSEBroker(nil, nil, nil, false)
	handler := NewProxyHandler(backend.Listener.Addr().String(), broker, es, nil, nil, nil, false)
	proxy := httptest.NewServer(handler)
	defer proxy.Close()

	req, err := http.NewRequest(http.MethodGet, proxy.URL, nil)
	require.NoError(t, err)
	req.Header.Set("Accept", "application/json")

	resp, err := http.DefaultClient.Do(req)
	require.NoError(t, err)
	defer func() { _ = resp.Body.Close() }()

	assert.Equal(t, http.StatusOK, resp.StatusCode)
	body, err := io.ReadAll(resp.Body)
	require.NoError(t, err)
	assert.Equal(t, `{"ok":true}`, string(body))
}

func TestErrorPage_SkippedForHTMX(t *testing.T) {
	backend := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "text/html")
		_, _ = w.Write([]byte("<div>partial</div>"))
	}))
	defer backend.Close()

	es := NewErrorState()
	es.Set("go", "error")
	broker := NewSSEBroker(nil, nil, nil, false)
	handler := NewProxyHandler(backend.Listener.Addr().String(), broker, es, nil, nil, nil, false)
	proxy := httptest.NewServer(handler)
	defer proxy.Close()

	req, err := http.NewRequest(http.MethodGet, proxy.URL, nil)
	require.NoError(t, err)
	req.Header.Set("Accept", "text/html")
	req.Header.Set("HX-Request", "true")

	resp, err := http.DefaultClient.Do(req)
	require.NoError(t, err)
	defer func() { _ = resp.Body.Close() }()

	assert.Equal(t, http.StatusOK, resp.StatusCode)
	body, err := io.ReadAll(resp.Body)
	require.NoError(t, err)
	assert.Equal(t, "<div>partial</div>", string(body))
}

func TestErrorPage_NotServedWhenNoErrors(t *testing.T) {
	backend := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "text/html")
		_, _ = w.Write([]byte("<html><body>OK</body></html>"))
	}))
	defer backend.Close()

	es := NewErrorState()
	broker := NewSSEBroker(nil, nil, nil, false)
	handler := NewProxyHandler(backend.Listener.Addr().String(), broker, es, nil, nil, nil, false)
	proxy := httptest.NewServer(handler)
	defer proxy.Close()

	req, err := http.NewRequest(http.MethodGet, proxy.URL, nil)
	require.NoError(t, err)
	req.Header.Set("Accept", "text/html")

	resp, err := http.DefaultClient.Do(req)
	require.NoError(t, err)
	defer func() { _ = resp.Body.Close() }()

	assert.Equal(t, http.StatusOK, resp.StatusCode)
	body, err := io.ReadAll(resp.Body)
	require.NoError(t, err)
	assert.Contains(t, string(body), "<html><body>OK</body></html>")
}

func TestLogsEndpoint(t *testing.T) {
	logBuf := NewLogBuffer(100)
	logBuf.Append(LogLine{Rule: "go", Text: "building..."})
	logBuf.Append(LogLine{Rule: "templ", Text: "generating templates"})

	broker := NewSSEBroker(nil, nil, nil, false)
	handler := NewProxyHandler("localhost:9999", broker, nil, logBuf, nil, nil, false)
	proxy := httptest.NewServer(handler)
	defer proxy.Close()

	resp, err := http.Get(proxy.URL + "/__hamr/logs")
	require.NoError(t, err)
	defer func() { _ = resp.Body.Close() }()

	assert.Equal(t, "application/json", resp.Header.Get("Content-Type"))

	var lines []LogLine
	err = json.NewDecoder(resp.Body).Decode(&lines)
	require.NoError(t, err)
	require.Len(t, lines, 2)
	assert.Equal(t, "go", lines[0].Rule)
	assert.Equal(t, "building...", lines[0].Text)
	assert.Equal(t, "templ", lines[1].Rule)
	assert.Equal(t, "generating templates", lines[1].Text)
}

func indexOf(s, sub string) int {
	for i := 0; i+len(sub) <= len(s); i++ {
		if s[i:i+len(sub)] == sub {
			return i
		}
	}
	return -1
}
