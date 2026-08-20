package devserver

import (
	"bufio"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestSSEBroker_Handler(t *testing.T) {
	broker := NewSSEBroker(nil, nil, nil, false, false, false, false)
	srv := httptest.NewServer(broker.Handler())
	defer srv.Close()

	resp, err := http.Get(srv.URL)
	require.NoError(t, err)
	defer func() { _ = resp.Body.Close() }()

	assert.Equal(t, "text/event-stream", resp.Header.Get("Content-Type"))
	assert.Equal(t, "no-cache", resp.Header.Get("Cache-Control"))

	// Read the initial "connected" event.
	scanner := bufio.NewScanner(resp.Body)
	var lines []string
	for scanner.Scan() {
		line := scanner.Text()
		lines = append(lines, line)
		if line == "" && len(lines) > 1 {
			break
		}
	}
	joined := strings.Join(lines, "\n")
	assert.Contains(t, joined, "event: connected")
	assert.Contains(t, joined, "data: ok")
}

func TestSSEBroker_Broadcast(t *testing.T) {
	broker := NewSSEBroker(nil, nil, nil, false, false, false, false)
	srv := httptest.NewServer(broker.Handler())
	defer srv.Close()

	resp, err := http.Get(srv.URL)
	require.NoError(t, err)
	defer func() { _ = resp.Body.Close() }()

	// Wait for client to connect.
	time.Sleep(50 * time.Millisecond)
	assert.Equal(t, 1, broker.ClientCount())

	// Broadcast an event.
	broker.Broadcast(SSEEvent{Type: "reload", Data: "full"})

	// Read events: connected + config + reload.
	scanner := bufio.NewScanner(resp.Body)
	events := readSSEEvents(scanner, 3)

	require.Len(t, events, 3)
	assert.Equal(t, "connected", events[0].typ)
	assert.Equal(t, "config", events[1].typ)
	assert.Equal(t, "reload", events[2].typ)
	assert.Equal(t, "full", events[2].data)
}

func TestSSEBroker_Broadcast_MultipleEvents(t *testing.T) {
	broker := NewSSEBroker(nil, nil, nil, false, false, false, false)
	srv := httptest.NewServer(broker.Handler())
	defer srv.Close()

	resp, err := http.Get(srv.URL)
	require.NoError(t, err)
	defer func() { _ = resp.Body.Close() }()

	time.Sleep(50 * time.Millisecond)

	broker.Broadcast(SSEEvent{Type: "reload", Data: "full"})
	broker.Broadcast(SSEEvent{Type: "reload", Data: "css"})

	scanner := bufio.NewScanner(resp.Body)
	events := readSSEEvents(scanner, 4) // connected + config + 2 reload events

	require.Len(t, events, 4)
	assert.Equal(t, "connected", events[0].typ)
	assert.Equal(t, "config", events[1].typ)
	assert.Equal(t, "reload", events[2].typ)
	assert.Equal(t, "full", events[2].data)
	assert.Equal(t, "reload", events[3].typ)
	assert.Equal(t, "css", events[3].data)
}

func TestSSEBroker_MultipleClients(t *testing.T) {
	broker := NewSSEBroker(nil, nil, nil, false, false, false, false)
	srv := httptest.NewServer(broker.Handler())
	defer srv.Close()

	// Connect two clients.
	resp1, err := http.Get(srv.URL)
	require.NoError(t, err)
	defer func() { _ = resp1.Body.Close() }()

	resp2, err := http.Get(srv.URL)
	require.NoError(t, err)
	defer func() { _ = resp2.Body.Close() }()

	time.Sleep(50 * time.Millisecond)
	assert.Equal(t, 2, broker.ClientCount())

	// Broadcast — both should receive.
	broker.Broadcast(SSEEvent{Type: "reload", Data: "full"})

	var wg sync.WaitGroup
	wg.Add(2)

	check := func(resp *http.Response) {
		defer wg.Done()
		scanner := bufio.NewScanner(resp.Body)
		events := readSSEEvents(scanner, 3) // connected + config + reload
		assert.Len(t, events, 3)
		if len(events) == 3 {
			assert.Equal(t, "reload", events[2].typ)
		}
	}

	go check(resp1)
	go check(resp2)
	wg.Wait()
}

func TestSSEBroker_ClientDisconnect(t *testing.T) {
	broker := NewSSEBroker(nil, nil, nil, false, false, false, false)
	srv := httptest.NewServer(broker.Handler())
	defer srv.Close()

	resp, err := http.Get(srv.URL)
	require.NoError(t, err)

	time.Sleep(50 * time.Millisecond)
	assert.Equal(t, 1, broker.ClientCount())

	// Close the response body to simulate disconnect.
	_ = resp.Body.Close()
	time.Sleep(100 * time.Millisecond)

	assert.Equal(t, 0, broker.ClientCount())
}

func TestSSEBroker_Broadcast_NoClients(t *testing.T) {
	broker := NewSSEBroker(nil, nil, nil, false, false, false, false)
	assert.Equal(t, 0, broker.ClientCount())

	// Should not panic or block.
	broker.Broadcast(SSEEvent{Type: "reload", Data: "full"})
}

func TestSSEBroker_Broadcast_FullChannel(t *testing.T) {
	broker := NewSSEBroker(nil, nil, nil, false, false, false, false)
	srv := httptest.NewServer(broker.Handler())
	defer srv.Close()

	resp, err := http.Get(srv.URL)
	require.NoError(t, err)
	defer func() { _ = resp.Body.Close() }()

	time.Sleep(50 * time.Millisecond)

	// Flood with more events than the channel buffer (16).
	// Should not block.
	done := make(chan struct{})
	go func() {
		for range 100 {
			broker.Broadcast(SSEEvent{Type: "reload", Data: "full"})
		}
		close(done)
	}()

	select {
	case <-done:
		// Good — broadcast didn't block.
	case <-time.After(2 * time.Second):
		t.Fatal("Broadcast blocked with full channel")
	}
}

func TestSSEBroker_ConfigEvent(t *testing.T) {
	rules := []WatchRule{
		{Name: "templ", Watch: StringOrSlice{"**/*.templ"}, Cmd: "templ generate", Reload: ReloadFull},
	}
	daemons := []Daemon{
		{Name: "server", Cmd: "go run ./cmd/site"},
	}
	broker := NewSSEBroker(rules, daemons, nil, false, false, false, false)
	srv := httptest.NewServer(broker.Handler())
	defer srv.Close()

	resp, err := http.Get(srv.URL)
	require.NoError(t, err)
	defer func() { _ = resp.Body.Close() }()

	scanner := bufio.NewScanner(resp.Body)
	events := readSSEEvents(scanner, 2) // connected + config

	require.Len(t, events, 2)
	assert.Equal(t, "config", events[1].typ)
	assert.Contains(t, events[1].data, `"templ"`)
	assert.Contains(t, events[1].data, `"server"`)
	assert.Contains(t, events[1].data, `"templ generate"`)
	assert.Contains(t, events[1].data, `"go run ./cmd/site"`)
}

func TestSSEBroker_ClientCount(t *testing.T) {
	broker := NewSSEBroker(nil, nil, nil, false, false, false, false)
	assert.Equal(t, 0, broker.ClientCount())
}

// TestSSEBroker_ConfigEvent_MailMockFlag asserts the config SSE payload
// carries the mail_mock flag so the dev-panel can decide whether to render
// the inbox shortcut.
func TestSSEBroker_ConfigEvent_MailMockFlag(t *testing.T) {
	t.Run("enabled", func(t *testing.T) {
		broker := NewSSEBroker(nil, nil, nil, true, false, false, false)
		srv := httptest.NewServer(broker.Handler())
		defer srv.Close()

		resp, err := http.Get(srv.URL)
		require.NoError(t, err)
		defer func() { _ = resp.Body.Close() }()

		events := readSSEEvents(bufio.NewScanner(resp.Body), 2)
		require.Len(t, events, 2)
		assert.Contains(t, events[1].data, `"mail_mock":true`)
	})

	t.Run("disabled omits key", func(t *testing.T) {
		broker := NewSSEBroker(nil, nil, nil, false, false, false, false)
		srv := httptest.NewServer(broker.Handler())
		defer srv.Close()

		resp, err := http.Get(srv.URL)
		require.NoError(t, err)
		defer func() { _ = resp.Body.Close() }()

		events := readSSEEvents(bufio.NewScanner(resp.Body), 2)
		require.Len(t, events, 2)
		assert.NotContains(t, events[1].data, `"mail_mock"`)
	})
}

// TestSSEBroker_ConfigEvent_StripeMockFlag mirrors the mail_mock test for
// the Stripe-mock dashboard shortcut. Same omit-when-false convention so
// the dev panel can branch on presence rather than parse a bool.
func TestSSEBroker_ConfigEvent_StripeMockFlag(t *testing.T) {
	t.Run("enabled", func(t *testing.T) {
		broker := NewSSEBroker(nil, nil, nil, false, false, true, false)
		srv := httptest.NewServer(broker.Handler())
		defer srv.Close()

		resp, err := http.Get(srv.URL)
		require.NoError(t, err)
		defer func() { _ = resp.Body.Close() }()

		events := readSSEEvents(bufio.NewScanner(resp.Body), 2)
		require.Len(t, events, 2)
		assert.Contains(t, events[1].data, `"stripe_mock":true`)
	})

	t.Run("disabled omits key", func(t *testing.T) {
		broker := NewSSEBroker(nil, nil, nil, false, false, false, false)
		srv := httptest.NewServer(broker.Handler())
		defer srv.Close()

		resp, err := http.Get(srv.URL)
		require.NoError(t, err)
		defer func() { _ = resp.Body.Close() }()

		events := readSSEEvents(bufio.NewScanner(resp.Body), 2)
		require.Len(t, events, 2)
		assert.NotContains(t, events[1].data, `"stripe_mock"`)
	})
}

type sseEvent struct {
	typ  string
	data string
}

func readSSEEvents(scanner *bufio.Scanner, count int) []sseEvent {
	var events []sseEvent
	var current sseEvent

	for scanner.Scan() {
		line := scanner.Text()

		if line == "" {
			if current.typ != "" {
				events = append(events, current)
				current = sseEvent{}
				if len(events) >= count {
					break
				}
			}
			continue
		}

		if after, ok := strings.CutPrefix(line, "event: "); ok {
			current.typ = after
		} else if after, ok := strings.CutPrefix(line, "data: "); ok {
			current.data = after
		}
	}
	return events
}
