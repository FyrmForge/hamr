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
	broker := NewSSEBroker(nil, nil, nil, false, false, false, false, false)
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
	broker := NewSSEBroker(nil, nil, nil, false, false, false, false, false)
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
	broker := NewSSEBroker(nil, nil, nil, false, false, false, false, false)
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
	broker := NewSSEBroker(nil, nil, nil, false, false, false, false, false)
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
	broker := NewSSEBroker(nil, nil, nil, false, false, false, false, false)
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
	broker := NewSSEBroker(nil, nil, nil, false, false, false, false, false)
	assert.Equal(t, 0, broker.ClientCount())

	// Should not panic or block.
	broker.Broadcast(SSEEvent{Type: "reload", Data: "full"})
}

func TestSSEBroker_Broadcast_FullChannel(t *testing.T) {
	broker := NewSSEBroker(nil, nil, nil, false, false, false, false, false)
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
	broker := NewSSEBroker(rules, daemons, nil, false, false, false, false, false)
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
	broker := NewSSEBroker(nil, nil, nil, false, false, false, false, false)
	assert.Equal(t, 0, broker.ClientCount())
}

// TestSSEBroker_ConfigEvent_MailMockFlag asserts the config SSE payload
// carries the mail_mock flag so the dev-panel can decide whether to render
// the inbox shortcut.
func TestSSEBroker_ConfigEvent_MailMockFlag(t *testing.T) {
	t.Run("enabled", func(t *testing.T) {
		broker := NewSSEBroker(nil, nil, nil, true, false, false, false, false)
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
		broker := NewSSEBroker(nil, nil, nil, false, false, false, false, false)
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
		broker := NewSSEBroker(nil, nil, nil, false, false, true, false, false)
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
		broker := NewSSEBroker(nil, nil, nil, false, false, false, false, false)
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

// TestSSEBroker_DarkFilterOnConnect asserts a tab connecting while the dark
// filter is on gets a dark_filter event straight after config — otherwise a
// page reload would silently drop back to the [dev].dark_filter seed.
func TestSSEBroker_DarkFilterOnConnect(t *testing.T) {
	broker := NewSSEBroker(nil, nil, nil, false, false, false, false, true)
	srv := httptest.NewServer(broker.Handler())
	defer srv.Close()

	resp, err := http.Get(srv.URL)
	require.NoError(t, err)
	defer func() { _ = resp.Body.Close() }()

	events := readSSEEvents(bufio.NewScanner(resp.Body), 3)
	require.Len(t, events, 3)
	assert.Equal(t, "dark_filter", events[2].typ)
	assert.Equal(t, "on", events[2].data)
}

// TestSSEBroker_Subscribe covers the in-process consumer path the TUI uses:
// events arrive, cancel removes and closes the channel, and a broadcast after
// cancel neither blocks nor panics on the closed channel.
func TestSSEBroker_Subscribe(t *testing.T) {
	broker := NewSSEBroker(nil, nil, nil, false, false, false, false, false)

	ch, cancel := broker.Subscribe()
	broker.Broadcast(SSEEvent{Type: EvBuilding, Data: "site"})

	select {
	case evt := <-ch:
		assert.Equal(t, EvBuilding, evt.Type)
		assert.Equal(t, "site", evt.Data)
	case <-time.After(time.Second):
		t.Fatal("subscriber never received the broadcast")
	}

	cancel()
	if _, open := <-ch; open {
		t.Fatal("cancel must close the subscriber channel so forwarders exit")
	}
	assert.Equal(t, 0, broker.ClientCount())

	// Must not send on the closed channel.
	broker.Broadcast(SSEEvent{Type: EvBuildOK, Data: "site"})

	// Cancel is idempotent — the TUI calls the stored cancel on every restart.
	cancel()
}

// TestSSEBroker_SubscribeDropsWhenFull guards the non-blocking contract for
// in-process consumers: a stalled TUI must not wedge a build goroutine.
func TestSSEBroker_SubscribeDropsWhenFull(t *testing.T) {
	broker := NewSSEBroker(nil, nil, nil, false, false, false, false, false)
	_, cancel := broker.Subscribe()
	defer cancel()

	done := make(chan struct{})
	go func() {
		for range 100 { // buffer is 16
			broker.Broadcast(SSEEvent{Type: EvOutput, Data: "x"})
		}
		close(done)
	}()
	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("Broadcast blocked on a full in-process subscriber")
	}
}

// TestSSEBroker_SubscribeExcludeKeepsLifecycle reproduces the stuck status-bar
// spinner: a restart floods the bus with process output, and on an unfiltered
// subscription that burst overflows the 16-slot buffer and drops the build_ok
// that clears "building site". Excluding output must keep the lifecycle event.
func TestSSEBroker_SubscribeExcludeKeepsLifecycle(t *testing.T) {
	broker := NewSSEBroker(nil, nil, nil, false, false, false, false, false)
	events, cancel := broker.Subscribe(EvOutput)
	defer cancel()

	for range 100 { // buffer is 16
		broker.Broadcast(outputEvent("site", "janitor: task completed", ""))
	}
	broker.Broadcast(SSEEvent{Type: EvBuildOK, Data: "site"})

	select {
	case evt := <-events:
		assert.Equal(t, EvBuildOK, evt.Type, "output must not reach a subscriber that excluded it")
		assert.Equal(t, "site", evt.Data)
	default:
		t.Fatal("build_ok dropped: the status bar would spin forever")
	}
}

// TestSSEBroker_BroadcastEvictsForStateEvents covers the consumer that cannot
// use the exclusion: the browser dev panel renders the log overlay, so it must
// keep EvOutput and its buffer fills during a rebuild. A build_ok arriving then
// has to evict rather than be dropped, or the panel's dot spins until something
// else resets it — and a rule with reload "none" never sends that reset.
func TestSSEBroker_BroadcastEvictsForStateEvents(t *testing.T) {
	broker := NewSSEBroker(nil, nil, nil, false, false, false, false, false)
	events, cancel := broker.Subscribe() // no exclusion, like the browser
	defer cancel()

	for range 100 { // buffer is 16
		broker.Broadcast(outputEvent("site", "janitor: task completed", ""))
	}
	broker.Broadcast(SSEEvent{Type: EvBuildOK, Data: "site"})

	var got []SSEEvent
	for len(events) > 0 {
		got = append(got, <-events)
	}
	require.NotEmpty(t, got)
	last := got[len(got)-1]
	assert.Equal(t, EvBuildOK, last.Type, "build_ok must survive a full buffer")
	assert.Equal(t, "site", last.Data)
	assert.Len(t, got, 16, "eviction must not grow the buffer")
}

// TestSSEBroker_BroadcastDropsOutputWhenFull is the other half of the policy:
// output stays droppable, so a log burst cannot push state events out.
func TestSSEBroker_BroadcastDropsOutputWhenFull(t *testing.T) {
	broker := NewSSEBroker(nil, nil, nil, false, false, false, false, false)
	events, cancel := broker.Subscribe()
	defer cancel()

	broker.Broadcast(SSEEvent{Type: EvBuilding, Data: "site"})
	for range 100 {
		broker.Broadcast(outputEvent("site", "line", ""))
	}

	first := <-events
	assert.Equal(t, EvBuilding, first.Type, "output must not evict a queued state event")
}

// TestSSEBroker_BroadcastConcurrentEvictKeepsStateEvent covers the shape the
// dev server actually runs: several logWriter goroutines flood the bus while
// builds report results. Broadcast holds only a read lock, so the evict and
// the retry in deliver must be serialized per client — otherwise a flooder
// takes the slot an evict just freed, and the state event is lost after
// destroying an older one, which is worse than dropping it outright.
//
// The buffer is prefilled with output and never drained, and far fewer state
// events are sent than the buffer holds. Channels are FIFO, so every eviction
// takes an output line and no state event can displace another: with delivery
// serialized, all of them must be present at the end.
func TestSSEBroker_BroadcastConcurrentEvictKeepsStateEvent(t *testing.T) {
	const (
		states  = 8
		writers = 4
		lines   = 2000 // per writer, to keep hitting the full buffer
	)

	broker := NewSSEBroker(nil, nil, nil, false, false, false, false, false)
	events, cancel := broker.Subscribe()
	defer cancel()

	for range 16 { // fill the buffer, so every send below finds it full
		broker.Broadcast(outputEvent("site", "prefill", ""))
	}

	var wg sync.WaitGroup
	for range writers {
		wg.Go(func() {
			for range lines {
				broker.Broadcast(outputEvent("site", "line", ""))
			}
		})
	}
	for range states {
		wg.Go(func() {
			broker.Broadcast(SSEEvent{Type: EvBuildOK, Data: "site"})
		})
	}
	wg.Wait()

	var seen int
	for len(events) > 0 {
		if (<-events).Type == EvBuildOK {
			seen++
		}
	}
	assert.Equal(t, states, seen,
		"state events lost to a concurrent output burst: evict and retry must be atomic per client")
}
