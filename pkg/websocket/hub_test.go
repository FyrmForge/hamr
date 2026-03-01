package websocket

import (
	"log/slog"
	"net/http"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestHub_Handler_upgradesConnection(t *testing.T) {
	hub, url, _, cleanup := setupTestHubSeq(t)
	defer cleanup()

	conn := dialWS(t, url, nil)
	defer func() { _ = conn.CloseNow() }()

	assert.Equal(t, 1, hub.Stats().Clients)

	hub.Broadcast([]byte(`{"type":"hello"}`))
	got := readJSON(t, conn)
	assert.Equal(t, "hello", got["type"])
}

func TestHub_SendToSession(t *testing.T) {
	hub, url, peek, cleanup := setupTestHubSeq(t)
	defer cleanup()

	sid1 := peek()
	conn1 := dialWS(t, url, nil)
	defer func() { _ = conn1.CloseNow() }()

	_ = peek()
	conn2 := dialWS(t, url, nil)
	defer func() { _ = conn2.CloseNow() }()

	hub.SendToSession(sid1, []byte(`{"type":"targeted"}`))

	got := readJSON(t, conn1)
	assert.Equal(t, "targeted", got["type"])
	assertNoMessage(t, conn2)
}

func TestHub_SendToSubject(t *testing.T) {
	hub, url, _, cleanup := setupTestHubSeq(t, WithSubjectIDFunc(subjectFromHeader))
	defer cleanup()

	hdr := http.Header{"X-Subject-Id": {"user-x"}}
	conn1 := dialWS(t, url, hdr)
	defer func() { _ = conn1.CloseNow() }()
	conn2 := dialWS(t, url, hdr)
	defer func() { _ = conn2.CloseNow() }()

	// Third client with different subject.
	conn3 := dialWS(t, url, nil)
	defer func() { _ = conn3.CloseNow() }()

	hub.SendToSubject("user-x", []byte(`{"type":"for-user-x"}`))

	got1 := readJSON(t, conn1)
	got2 := readJSON(t, conn2)
	assert.Equal(t, "for-user-x", got1["type"])
	assert.Equal(t, "for-user-x", got2["type"])
	assertNoMessage(t, conn3)
}

func TestHub_SendToSubject_noAuth(t *testing.T) {
	hub, _, _, cleanup := setupTestHubSeq(t)
	defer cleanup()

	// No subject ID func set, empty subject should no-op gracefully.
	hub.SendToSubject("", []byte(`{"type":"noop"}`))
	hub.SendToSubject("nonexistent", []byte(`{"type":"noop"}`))
}

func TestHub_SendToRoom(t *testing.T) {
	hub, url, peek, cleanup := setupTestHubSeq(t)
	defer cleanup()

	sid1 := peek()
	conn1 := dialWS(t, url, nil)
	defer func() { _ = conn1.CloseNow() }()

	sid2 := peek()
	conn2 := dialWS(t, url, nil)
	defer func() { _ = conn2.CloseNow() }()

	_ = peek()
	conn3 := dialWS(t, url, nil)
	defer func() { _ = conn3.CloseNow() }()

	hub.JoinRoom(getClient(t, hub, sid1), "game-42")
	hub.JoinRoom(getClient(t, hub, sid2), "game-42")

	hub.SendToRoom("game-42", []byte(`{"type":"room-msg"}`))

	got1 := readJSON(t, conn1)
	got2 := readJSON(t, conn2)
	assert.Equal(t, "room-msg", got1["type"])
	assert.Equal(t, "room-msg", got2["type"])
	assertNoMessage(t, conn3)
}

func TestHub_SendToRoomExcept(t *testing.T) {
	hub, url, peek, cleanup := setupTestHubSeq(t)
	defer cleanup()

	sid1 := peek()
	conn1 := dialWS(t, url, nil)
	defer func() { _ = conn1.CloseNow() }()

	sid2 := peek()
	conn2 := dialWS(t, url, nil)
	defer func() { _ = conn2.CloseNow() }()

	hub.JoinRoom(getClient(t, hub, sid1), "chat")
	hub.JoinRoom(getClient(t, hub, sid2), "chat")

	hub.SendToRoomExcept("chat", []byte(`{"type":"except"}`), sid1)

	got := readJSON(t, conn2)
	assert.Equal(t, "except", got["type"])
	assertNoMessage(t, conn1)
}

func TestHub_Broadcast(t *testing.T) {
	hub, url, _, cleanup := setupTestHubSeq(t)
	defer cleanup()

	conn1 := dialWS(t, url, nil)
	defer func() { _ = conn1.CloseNow() }()
	conn2 := dialWS(t, url, nil)
	defer func() { _ = conn2.CloseNow() }()
	conn3 := dialWS(t, url, nil)
	defer func() { _ = conn3.CloseNow() }()

	hub.Broadcast([]byte(`{"type":"all"}`))

	assert.Equal(t, "all", readJSON(t, conn1)["type"])
	assert.Equal(t, "all", readJSON(t, conn2)["type"])
	assert.Equal(t, "all", readJSON(t, conn3)["type"])
}

func TestHub_JoinRoom_LeaveRoom(t *testing.T) {
	hub, url, peek, cleanup := setupTestHubSeq(t)
	defer cleanup()

	sid := peek()
	conn := dialWS(t, url, nil)
	defer func() { _ = conn.CloseNow() }()

	c := getClient(t, hub, sid)
	hub.JoinRoom(c, "room-a")

	hub.SendToRoom("room-a", []byte(`{"type":"joined"}`))
	got := readJSON(t, conn)
	assert.Equal(t, "joined", got["type"])

	hub.LeaveRoom(c, "room-a")
	hub.SendToRoom("room-a", []byte(`{"type":"after-leave"}`))
	assertNoMessage(t, conn)
}

func TestHub_AssociateSubject(t *testing.T) {
	hub, url, peek, cleanup := setupTestHubSeq(t)
	defer cleanup()

	sid := peek()
	conn := dialWS(t, url, nil)
	defer func() { _ = conn.CloseNow() }()

	// No subject yet — verify stats show 0 subjects.
	assert.Equal(t, 0, hub.Stats().Subjects)

	// Associate at runtime.
	hub.AssociateSubject(sid, "late-user")
	assert.Equal(t, 1, hub.Stats().Subjects)

	hub.SendToSubject("late-user", []byte(`{"type":"yes"}`))
	got := readJSON(t, conn)
	assert.Equal(t, "yes", got["type"])
}

func TestHub_clientDisconnect_unregisters(t *testing.T) {
	hub, url, peek, cleanup := setupTestHubSeq(t)
	defer cleanup()

	sid := peek()
	conn := dialWS(t, url, nil)

	c := getClient(t, hub, sid)
	hub.JoinRoom(c, "temp-room")

	assert.Equal(t, 1, hub.Stats().Clients)
	assert.Equal(t, 1, hub.Stats().Rooms)

	_ = conn.CloseNow()
	// Wait for unregister.
	waitFor(t, func() bool { return hub.Stats().Clients == 0 })

	assert.Equal(t, 0, hub.Stats().Clients)
	assert.Equal(t, 0, hub.Stats().Rooms)
}

func TestHub_Close_graceful(t *testing.T) {
	hub, url, _, cleanup := setupTestHubSeq(t)

	conn1 := dialWS(t, url, nil)
	defer func() { _ = conn1.CloseNow() }()
	conn2 := dialWS(t, url, nil)
	defer func() { _ = conn2.CloseNow() }()

	assert.Equal(t, 2, hub.Stats().Clients)

	cleanup() // calls hub.Close() + srv.Close()

	assert.Equal(t, 0, hub.Stats().Clients)
}

func TestHub_Close_idempotent(t *testing.T) {
	hub := NewHub()
	hub.Close()
	hub.Close() // should not panic
}

func TestHub_Stats(t *testing.T) {
	hub, url, peek, cleanup := setupTestHubSeq(t, WithSubjectIDFunc(subjectFromHeader))
	defer cleanup()

	hdr := http.Header{"X-Subject-Id": {"u1"}}
	sid1 := peek()
	conn1 := dialWS(t, url, hdr)
	defer func() { _ = conn1.CloseNow() }()

	_ = peek()
	conn2 := dialWS(t, url, nil)
	defer func() { _ = conn2.CloseNow() }()

	hub.JoinRoom(getClient(t, hub, sid1), "r1")

	s := hub.Stats()
	assert.Equal(t, 2, s.Clients)
	assert.Equal(t, 1, s.Subjects)
	assert.Equal(t, 1, s.Rooms)
}

func TestHub_WithOnMessage(t *testing.T) {
	received := make(chan []byte, 1)
	onMsg := func(_ *Client, data []byte) {
		received <- data
	}

	_, url, _, cleanup := setupTestHubSeq(t, WithOnMessage(onMsg))
	defer cleanup()

	conn := dialWS(t, url, nil)
	defer func() { _ = conn.CloseNow() }()

	require.NoError(t, conn.Write(t.Context(), 1, []byte(`{"action":"ping"}`)))

	select {
	case msg := <-received:
		assert.JSONEq(t, `{"action":"ping"}`, string(msg))
	case <-t.Context().Done():
		t.Fatal("timed out waiting for onMessage callback")
	}
}

func TestHub_sendBufferFull_dropsMessage(t *testing.T) {
	hub := NewHub(WithLogger(slog.Default()))
	defer hub.Close()

	// Create a client with a full send buffer but no running writePump.
	c := &Client{
		SessionID: "full-buf",
		Rooms:     make(map[string]bool),
		Meta:      make(map[string]any),
		hub:       hub,
		send:      make(chan []byte, sendBufferSize),
	}

	hub.mu.Lock()
	hub.clients[c.SessionID] = c
	hub.mu.Unlock()

	// Fill the buffer.
	for range sendBufferSize {
		c.send <- []byte(`filler`)
	}

	// safeSend should drop rather than block.
	done := make(chan struct{})
	go func() {
		hub.SendToSession("full-buf", []byte(`dropped`))
		close(done)
	}()

	select {
	case <-done:
		// Good — returned without blocking.
	case <-t.Context().Done():
		t.Fatal("SendToSession blocked on full buffer")
	}

	assert.Equal(t, sendBufferSize, len(c.send))
}

// waitFor polls a condition for up to 500ms.
func waitFor(t *testing.T, cond func() bool) {
	t.Helper()
	deadline := t.Context()
	for !cond() {
		select {
		case <-deadline.Done():
			t.Fatal("waitFor timed out")
			return
		default:
		}
	}
}
