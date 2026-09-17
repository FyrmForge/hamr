package devserver

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestTunnelURLScan(t *testing.T) {
	tests := []struct {
		name   string
		cfg    TunnelConfig
		output string
		want   string
	}{
		{
			name: "cloudflared skips banner links",
			cfg:  TunnelConfig{},
			output: "2026-09-16T10:00:00Z INF Thank you for trying Cloudflare Tunnel. See https://developers.cloudflare.com/cloudflare-one/connections/connect-apps/install-and-setup/tunnel-guide/\n" +
				"2026-09-16T10:00:00Z INF Requesting new quick Tunnel on trycloudflare.com...\n" +
				"2026-09-16T10:00:01Z INF |  https://calm-river-bold-owl.trycloudflare.com                                             |\n",
			want: "https://calm-river-bold-owl.trycloudflare.com",
		},
		{
			name: "ngrok logfmt",
			cfg:  TunnelConfig{Provider: TunnelNgrok},
			output: `t=2026-09-16T10:00:00+0000 lvl=info msg="open config file" path=/home/u/.config/ngrok/ngrok.yml err=nil` + "\n" +
				`t=2026-09-16T10:00:01+0000 lvl=info msg="started tunnel" obj=tunnels name=command_line addr=http://127.0.0.1:41234 url=https://abcd-1-2-3-4.ngrok-free.app` + "\n",
			want: "https://abcd-1-2-3-4.ngrok-free.app",
		},
		{
			name:   "custom cmd takes first https url",
			cfg:    TunnelConfig{Cmd: "ssh -R 80:localhost:{port} serveo.net"},
			output: "Forwarding HTTP traffic from https://abc.serveo.net.\n",
			want:   "https://abc.serveo.net",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, re := tt.cfg.command(1234)
			found := make(chan string, 1)
			s := &urlScanner{re: re, found: found}
			// Split mid-line to prove lines are reassembled across writes.
			half := len(tt.output) / 2
			_, _ = s.Write([]byte(tt.output[:half]))
			_, _ = s.Write([]byte(tt.output[half:]))
			select {
			case got := <-found:
				assert.Equal(t, tt.want, got)
			default:
				t.Fatal("no URL found")
			}
		})
	}
}

func TestTunnelCommand(t *testing.T) {
	argv, _ := TunnelConfig{Args: []string{"--region", "eu"}}.command(4000)
	assert.Equal(t, []string{"cloudflared", "tunnel", "--no-autoupdate", "--url", "http://127.0.0.1:4000", "--region", "eu"}, argv)

	argv, _ = TunnelConfig{Cmd: "tool --to localhost:{port}"}.command(4000)
	assert.Equal(t, []string{"sh", "-c", "tool --to localhost:4000"}, argv)
}

func TestBlockDevCommands(t *testing.T) {
	h := blockDevCommands(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if !isTunnelRequest(r) {
			w.WriteHeader(http.StatusTeapot) // passed through unmarked
		}
	}))
	for path, want := range map[string]int{
		"/__hamr/console":            http.StatusForbidden,
		"/__hamr/rule/site/run":      http.StatusForbidden,
		"/__hamr/docker/db/restart":  http.StatusForbidden,
		"/__hamr/mcp/make.run":       http.StatusForbidden,
		"/__hamr/logs":               http.StatusForbidden,
		"/x/../__hamr/rule/site/run": http.StatusForbidden,
		"/__hamr/dark":               http.StatusForbidden,
		"/__hamr/some-future-route":  http.StatusForbidden,
		"/__hamr":                    http.StatusForbidden,
		"/__hamr/mailx":              http.StatusForbidden,
		"/__hamr/mail":               http.StatusOK,
		"/__hamr/sms/clear":          http.StatusOK,
		"/__hamr/stripe/checkout":    http.StatusOK,
		"/__hamr/reload":             http.StatusOK,
		"/__hamr/logo.png":           http.StatusOK,
		"/v1/payment_intents":        http.StatusOK,
		"/__hamrx":                   http.StatusOK,
		"/":                          http.StatusOK,
	} {
		rec := httptest.NewRecorder()
		h.ServeHTTP(rec, httptest.NewRequest(http.MethodPost, path, nil))
		assert.Equal(t, want, rec.Code, path)
	}
}

func TestTunnelConfigValidate(t *testing.T) {
	require.NoError(t, TunnelConfig{}.validate())
	require.NoError(t, TunnelConfig{Provider: TunnelNgrok, Args: []string{"--url", "me.ngrok.app"}}.validate())
	require.NoError(t, TunnelConfig{Cmd: "x {port}"}.validate())

	for _, bad := range []TunnelConfig{
		{Provider: "bore"},
		{Cmd: "x"},
		{Cmd: "x {port}", Provider: TunnelNgrok},
		{Cmd: "x {port}", Args: []string{"-v"}},
	} {
		err := bad.validate()
		require.Error(t, err)
		assert.True(t, strings.HasPrefix(err.Error(), "[dev.tunnel]"), err.Error())
	}

	assert.Equal(t, []string{"BASE_URL"}, TunnelConfig{}.ResolvedEnv())
	assert.Empty(t, TunnelConfig{Env: []string{}}.ResolvedEnv())
}

func TestTunnelRedaction(t *testing.T) {
	_, ok := redactForTunnel(SSEEvent{Type: EvOutput, Data: `{"rule":"site","text":"DB_PASSWORD=hunter2"}`})
	assert.False(t, ok, "process output must not reach tunnel visitors")

	evt, ok := redactForTunnel(buildErrorEvent("site", "secret stack trace"))
	require.True(t, ok)
	assert.Contains(t, evt.Data, `"rule":"site"`)
	assert.NotContains(t, evt.Data, "secret")

	evt, ok = redactForTunnel(SSEEvent{Type: EvReload, Data: "full"})
	assert.True(t, ok)
	assert.Equal(t, "full", evt.Data)

	b := NewSSEBroker(
		[]WatchRule{{Name: "site", Cmd: "go build -ldflags=-X=main.key=abc", Run: "./bin/site", Watch: []string{"**/*.go"}}},
		[]Daemon{{Name: "worker", Cmd: "TOKEN=xyz ./worker"}},
		[]DockerCompose{{Name: "deps", File: "docker/compose.yaml"}},
		true, false, false, true, false,
	)
	for _, leak := range []string{"ldflags", "./bin/site", "*.go", "TOKEN", "compose.yaml", `"console_capture"`} {
		assert.NotContains(t, b.tunnelConfigJSON, leak)
	}
	for _, keep := range []string{`"site"`, `"worker"`, `"deps"`, `"mail_mock":true`} {
		assert.Contains(t, b.tunnelConfigJSON, keep)
	}

	// Error page: tunnel visitors see the rule, not its output.
	es := NewErrorState()
	es.Set("site", "secret stack trace")
	h := blockDevCommands(&errorInterceptor{errorState: es, next: http.NotFoundHandler()})
	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/", nil)
	req.Header.Set("Accept", "text/html")
	h.ServeHTTP(rec, req)
	assert.Contains(t, rec.Body.String(), "site")
	assert.NotContains(t, rec.Body.String(), "secret stack trace")
}
