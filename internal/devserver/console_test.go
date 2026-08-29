package devserver

import (
	"bytes"
	"context"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	ws "github.com/coder/websocket"
)

// stripANSI is defined in errorpage.go.

func TestConsoleSink_Write_PlainLog(t *testing.T) {
	var buf bytes.Buffer
	sink := NewConsoleSink(&buf, false)

	sink.Write(ConsoleFrame{Level: "log", Msg: "hello world"})

	plain := stripANSI(buf.String())
	assert.Equal(t, "[site:console] hello world\r\n", plain)
}

func TestConsoleSink_Write_WarnAddsLevelLabel(t *testing.T) {
	var buf bytes.Buffer
	sink := NewConsoleSink(&buf, false)

	sink.Write(ConsoleFrame{Level: "warn", Msg: "deprecated"})

	plain := stripANSI(buf.String())
	assert.Equal(t, "[site:console] WARN deprecated\r\n", plain)
}

func TestConsoleSink_Write_ErrorAddsLevelLabel(t *testing.T) {
	var buf bytes.Buffer
	sink := NewConsoleSink(&buf, false)

	sink.Write(ConsoleFrame{Level: "error", Msg: "boom"})

	plain := stripANSI(buf.String())
	assert.Equal(t, "[site:console] ERROR boom\r\n", plain)
}

func TestConsoleSink_Write_InfoAndDebugRenderUnlabeled(t *testing.T) {
	cases := []string{"info", "debug", "log", "rejection", "resource", "csp"}
	for _, level := range cases {
		var buf bytes.Buffer
		sink := NewConsoleSink(&buf, false)

		sink.Write(ConsoleFrame{Level: level, Msg: "x"})

		plain := stripANSI(buf.String())
		// Only warn and error get a level label per the agreed format.
		// Anything else (including custom categories like rejection/csp)
		// renders without a colored level word so the line stays scannable.
		assert.Equal(t, "[site:console] x\r\n", plain, "level=%q", level)
	}
}

func TestConsoleSink_Write_AppendsSrcWhenPresent(t *testing.T) {
	var buf bytes.Buffer
	sink := NewConsoleSink(&buf, false)

	sink.Write(ConsoleFrame{Level: "error", Msg: "TypeError: x is undefined", Src: "app.js:42:7"})

	plain := stripANSI(buf.String())
	assert.Equal(t, "[site:console] ERROR TypeError: x is undefined @ app.js:42:7\r\n", plain)
}

func TestConsoleSink_Write_DropsEmptyMsg(t *testing.T) {
	var buf bytes.Buffer
	sink := NewConsoleSink(&buf, false)

	sink.Write(ConsoleFrame{Level: "log", Msg: ""})

	assert.Empty(t, buf.String(), "empty messages produce no output line")
}

func TestConsoleSink_Write_FilterDropsHamrTag(t *testing.T) {
	var buf bytes.Buffer
	sink := NewConsoleSink(&buf, true)

	sink.Write(ConsoleFrame{Level: "log", Msg: "[hamr] page swapped"})
	sink.Write(ConsoleFrame{Level: "log", Msg: "user did a thing"})

	plain := stripANSI(buf.String())
	assert.NotContains(t, plain, "page swapped", "[hamr]-tagged frames are dropped when filter is on")
	assert.Contains(t, plain, "user did a thing", "non-tagged frames pass through")
}

func TestConsoleSink_Write_FilterOffKeepsHamrTag(t *testing.T) {
	var buf bytes.Buffer
	sink := NewConsoleSink(&buf, false)

	sink.Write(ConsoleFrame{Level: "log", Msg: "[hamr] page swapped"})

	plain := stripANSI(buf.String())
	assert.Contains(t, plain, "[hamr] page swapped", "filter off keeps [hamr]-tagged frames")
}

func TestConsoleSink_Ingest_SingleObject(t *testing.T) {
	var buf bytes.Buffer
	sink := NewConsoleSink(&buf, false)

	sink.ingest([]byte(`{"level":"warn","msg":"oops"}`))

	plain := stripANSI(buf.String())
	assert.Equal(t, "[site:console] WARN oops\r\n", plain)
}

func TestConsoleSink_Ingest_BatchArray(t *testing.T) {
	var buf bytes.Buffer
	sink := NewConsoleSink(&buf, false)

	sink.ingest([]byte(`[{"level":"log","msg":"a"},{"level":"log","msg":"b"}]`))

	plain := stripANSI(buf.String())
	assert.Contains(t, plain, "[site:console] a\r\n")
	assert.Contains(t, plain, "[site:console] b\r\n")
}

func TestConsoleSink_Ingest_GarbageDropped(t *testing.T) {
	var buf bytes.Buffer
	sink := NewConsoleSink(&buf, false)

	sink.ingest([]byte(`not json`))
	sink.ingest([]byte(``))
	sink.ingest([]byte(`   `))

	assert.Empty(t, buf.String(), "garbage and empty payloads are dropped silently")
}

func TestHamrConsoleCaptureEnabled_DefaultsTrue(t *testing.T) {
	var cfg DevConfig // pointer left nil
	assert.True(t, cfg.HamrConsoleCaptureEnabled(), "nil pointer means default-on")
}

func TestHamrConsoleCaptureEnabled_RespectsExplicitFalse(t *testing.T) {
	off := false
	cfg := DevConfig{HamrConsoleCapture: &off}
	assert.False(t, cfg.HamrConsoleCaptureEnabled(), "explicit false disables capture")
}

func TestHamrConsoleCaptureEnabled_RespectsExplicitTrue(t *testing.T) {
	on := true
	cfg := DevConfig{HamrConsoleCapture: &on}
	assert.True(t, cfg.HamrConsoleCaptureEnabled())
}

func TestSSEBroker_ConfigPayload_IncludesConsoleCaptureWhenOn(t *testing.T) {
	broker := NewSSEBroker(nil, nil, nil, false, false, false, true, false)

	srv := httptest.NewServer(broker.Handler())
	defer srv.Close()

	resp, err := srv.Client().Get(srv.URL)
	require.NoError(t, err)
	defer resp.Body.Close() //nolint:errcheck

	buf := make([]byte, 1024)
	n, _ := resp.Body.Read(buf)

	body := string(buf[:n])
	assert.Contains(t, body, `"console_capture":true`, "true flag is emitted into the config payload")
}

func TestSSEBroker_ConfigPayload_OmitsConsoleCaptureWhenOff(t *testing.T) {
	broker := NewSSEBroker(nil, nil, nil, false, false, false, false, false)

	srv := httptest.NewServer(broker.Handler())
	defer srv.Close()

	resp, err := srv.Client().Get(srv.URL)
	require.NoError(t, err)
	defer resp.Body.Close() //nolint:errcheck

	buf := make([]byte, 1024)
	n, _ := resp.Body.Read(buf)

	body := string(buf[:n])
	assert.NotContains(t, body, "console_capture", "false flag uses omitempty so the JS treats absence as off")
}

func TestConsoleSink_Handler_RoundTrip(t *testing.T) {
	var buf bytes.Buffer
	sink := NewConsoleSink(&buf, false)
	srv := httptest.NewServer(sink.Handler())
	defer srv.Close()

	wsURL := strings.Replace(srv.URL, "http://", "ws://", 1)

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	conn, _, err := ws.Dial(ctx, wsURL, nil)
	require.NoError(t, err, "ws dial succeeds against the sink handler")
	defer conn.CloseNow() //nolint:errcheck

	require.NoError(t, conn.Write(ctx, ws.MessageText, []byte(`[{"level":"error","msg":"network down","src":"app.js:1:1"}]`)))

	// The handler writes synchronously after Read returns; close the
	// connection cleanly so the read loop unblocks and we can assert.
	require.NoError(t, conn.Close(ws.StatusNormalClosure, ""))

	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		if buf.Len() > 0 {
			break
		}
		time.Sleep(10 * time.Millisecond)
	}

	plain := stripANSI(buf.String())
	assert.Equal(t, "[site:console] ERROR network down @ app.js:1:1\r\n", plain)
}
