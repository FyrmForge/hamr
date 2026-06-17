package websocket

import (
	"context"
	"sync"
	"testing"
	"time"

	ws "github.com/coder/websocket"
	"github.com/stretchr/testify/assert"
)

// TestHub_JoinRoom_staleClientIgnored guards against re-adding a disconnected
// client to a room: its send channel is already closed by unregister, so a
// subsequent SendToRoom would panic on send-to-closed-channel.
func TestHub_JoinRoom_staleClientIgnored(t *testing.T) {
	hub, url, peek, cleanup := setupTestHubSeq(t)
	defer cleanup()

	sid := peek()
	conn := dialWS(t, url, nil)
	c := getClient(t, hub, sid)

	// Disconnect: the client is unregistered and its send channel closed.
	_ = conn.CloseNow()
	waitFor(t, func() bool { return hub.Stats().Clients == 0 })

	// Re-adding the now-stale client must be a no-op...
	hub.JoinRoom(c, "ghost-room")
	assert.Equal(t, 0, hub.Stats().Rooms, "stale client must not be added to a room")

	// ...so SendToRoom can't panic sending to the closed channel.
	assert.NotPanics(t, func() {
		hub.SendToRoom("ghost-room", []byte(`{"type":"x"}`))
	})
}

// TestHub_Handler_rejectsAfterClose verifies that a connection arriving after
// Close does not register a client (which would repopulate the maps Close just
// cleared).
func TestHub_Handler_rejectsAfterClose(t *testing.T) {
	hub, url, cleanup := setupTestHub(t)
	defer cleanup()

	hub.Close()

	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	conn, _, err := ws.Dial(ctx, url, &ws.DialOptions{})
	if err == nil {
		_ = conn.CloseNow()
	}
	time.Sleep(30 * time.Millisecond)
	assert.Equal(t, 0, hub.Stats().Clients, "no client should register after Close")
}

// TestHub_Close_concurrentConnect hammers the handler with connection attempts
// while Close runs. The guarded hazard is Handler's wg.Add(1) racing Close's
// wg.Wait() (a WaitGroup contract violation) plus late registrations
// repopulating cleared maps. Most valuable under `-race`.
func TestHub_Close_concurrentConnect(t *testing.T) {
	hub, url, cleanup := setupTestHub(t)
	defer cleanup()

	var wg sync.WaitGroup
	for range 20 {
		wg.Go(func() {
			ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
			defer cancel()
			conn, _, err := ws.Dial(ctx, url, &ws.DialOptions{})
			if err == nil {
				_ = conn.CloseNow()
			}
		})
	}

	assert.NotPanics(t, func() { hub.Close() })
	wg.Wait()
	assert.Equal(t, 0, hub.Stats().Clients)
}
