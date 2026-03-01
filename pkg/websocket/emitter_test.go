package websocket

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"
	"time"

	ws "github.com/coder/websocket"
	"github.com/labstack/echo/v4"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestEmitter_ToSession(t *testing.T) {
	hub, url, peek, cleanup := setupTestHubSeq(t)
	defer cleanup()

	sid := peek()
	conn, err := dialWS(t, url, nil)
	require.NoError(t, err)
	defer conn.CloseNow()

	em := NewEmitter(hub)
	ev := NewEvent("test:msg", map[string]string{"k": "v"})
	em.ToSession(sid, ev)

	got := readJSON(t, conn)
	assert.Equal(t, "test:msg", got["type"])
}

func TestEmitter_ToSubject(t *testing.T) {
	hub, url, _, cleanup := setupTestHubSeq(t, WithSubjectIDFunc(subjectFromHeader))
	defer cleanup()

	hdr := http.Header{"X-Subject-Id": {"user-a"}}
	conn1, err := dialWS(t, url, hdr)
	require.NoError(t, err)
	defer conn1.CloseNow()
	conn2, err := dialWS(t, url, hdr)
	require.NoError(t, err)
	defer conn2.CloseNow()

	em := NewEmitter(hub)
	ev := NewHTMLEvent("html:update", "#box", "<b>hi</b>")
	em.ToSubject("user-a", ev)

	got1 := readJSON(t, conn1)
	got2 := readJSON(t, conn2)
	assert.Equal(t, "html:update", got1["type"])
	assert.Equal(t, "html:update", got2["type"])
}

func TestEmitter_ToRoom(t *testing.T) {
	hub, url, peek, cleanup := setupTestHubSeq(t)
	defer cleanup()

	sid1 := peek()
	conn1, err := dialWS(t, url, nil)
	require.NoError(t, err)
	defer conn1.CloseNow()

	_ = peek() // skip sid2
	conn2, err := dialWS(t, url, nil)
	require.NoError(t, err)
	defer conn2.CloseNow()

	c1 := getClient(t, hub, sid1)
	hub.JoinRoom(c1, "lobby")

	em := NewEmitter(hub)
	ev := NewTriggerEvent("notify:refresh", "#panel", "refresh")
	em.ToRoom("lobby", ev)

	got := readJSON(t, conn1)
	assert.Equal(t, "notify:refresh", got["type"])
	assertNoMessage(t, conn2)
}

func TestEmitter_ToRoomExcept(t *testing.T) {
	hub, url, peek, cleanup := setupTestHubSeq(t)
	defer cleanup()

	sid1 := peek()
	conn1, err := dialWS(t, url, nil)
	require.NoError(t, err)
	defer conn1.CloseNow()

	sid2 := peek()
	conn2, err := dialWS(t, url, nil)
	require.NoError(t, err)
	defer conn2.CloseNow()

	c1 := getClient(t, hub, sid1)
	c2 := getClient(t, hub, sid2)
	hub.JoinRoom(c1, "room")
	hub.JoinRoom(c2, "room")

	em := NewEmitter(hub)
	ev := NewEvent("chat:msg", "hello")
	em.ToRoomExcept("room", ev, sid1)

	got := readJSON(t, conn2)
	assert.Equal(t, "chat:msg", got["type"])
	assertNoMessage(t, conn1)
}

func TestEmitter_Broadcast(t *testing.T) {
	hub, url, _, cleanup := setupTestHubSeq(t)
	defer cleanup()

	conn1, err := dialWS(t, url, nil)
	require.NoError(t, err)
	defer conn1.CloseNow()
	conn2, err := dialWS(t, url, nil)
	require.NoError(t, err)
	defer conn2.CloseNow()

	em := NewEmitter(hub)
	ev := NewOuterHTMLEvent("sys:alert", "#banner", "<div>!</div>")
	em.Broadcast(ev)

	got1 := readJSON(t, conn1)
	got2 := readJSON(t, conn2)
	assert.Equal(t, "sys:alert", got1["type"])
	assert.Equal(t, "sys:alert", got2["type"])
}

// --- shared test helpers ---

// sequentialSessionID returns a WithSessionIDFunc option that assigns
// deterministic, incrementing session IDs ("s-1", "s-2", ...) and a peekNext
// function that returns the session ID that the *next* dial will receive.
func sequentialSessionID() (HubOption, func() string) {
	var counter atomic.Int64
	fn := func(_ *http.Request) string {
		return fmt.Sprintf("s-%d", counter.Add(1))
	}
	peekNext := func() string {
		return fmt.Sprintf("s-%d", counter.Load()+1)
	}
	return WithSessionIDFunc(fn), peekNext
}

// setupTestHub creates a Hub, mounts it on an httptest server, and returns
// the hub, ws:// URL, and a cleanup function.
func setupTestHub(t *testing.T, opts ...HubOption) (*Hub, string, func()) {
	t.Helper()
	hub := NewHub(opts...)
	e := echo.New()
	e.GET("/ws", hub.Handler())
	srv := httptest.NewServer(e)
	wsURL := "ws" + srv.URL[4:] + "/ws"
	cleanup := func() {
		hub.Close()
		srv.Close()
	}
	return hub, wsURL, cleanup
}

// setupTestHubSeq is like setupTestHub but installs deterministic session IDs.
// Returns an additional peekNext function.
func setupTestHubSeq(t *testing.T, extraOpts ...HubOption) (*Hub, string, func() string, func()) {
	t.Helper()
	seqOpt, peekNext := sequentialSessionID()
	opts := append([]HubOption{seqOpt}, extraOpts...)
	hub, url, cleanup := setupTestHub(t, opts...)
	return hub, url, peekNext, cleanup
}

// dialWS dials a WebSocket and waits for registration.
func dialWS(t *testing.T, url string, header http.Header) (*ws.Conn, error) {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	conn, _, err := ws.Dial(ctx, url, &ws.DialOptions{HTTPHeader: header})
	if err != nil {
		return nil, err
	}
	time.Sleep(30 * time.Millisecond)
	return conn, nil
}

func getClient(t *testing.T, hub *Hub, sessionID string) *Client {
	t.Helper()
	hub.mu.RLock()
	defer hub.mu.RUnlock()
	c, ok := hub.clients[sessionID]
	require.True(t, ok, "client %s not found", sessionID)
	return c
}

func readJSON(t *testing.T, conn *ws.Conn) map[string]any {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	_, data, err := conn.Read(ctx)
	require.NoError(t, err)
	var m map[string]any
	require.NoError(t, json.Unmarshal(data, &m))
	return m
}

func assertNoMessage(t *testing.T, conn *ws.Conn) {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 100*time.Millisecond)
	defer cancel()
	_, _, err := conn.Read(ctx)
	assert.Error(t, err, "expected no message but got one")
}

func subjectFromHeader(r *http.Request) string {
	return r.Header.Get("X-Subject-Id")
}
