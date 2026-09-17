package devserver

import (
	"context"
	"io"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// injected returns the value of key in the process manager's injected env,
// last-wins like buildEnv.
func injected(pm *ProcessManager, key string) string {
	pm.mu.Lock()
	defer pm.mu.Unlock()
	val := ""
	for _, e := range pm.injectedEnv {
		if k, v, ok := strings.Cut(e, "="); ok && k == key {
			val = v
		}
	}
	return val
}

func TestEnvLayersOrder(t *testing.T) {
	pm := NewProcessManager(slog.New(slog.NewTextHandler(io.Discard, nil)))
	e := &envLayers{ctx: context.Background(), cfg: &Config{}, pm: pm, logger: slog.Default()}
	e.setBase([]string{"PORT=8080", "BASE_URL=http://localhost:3000", "STRIPE_MOCK=true"})
	e.setStripe([]string{"STRIPE_MOCK=false"}, false)
	e.setTunnel([]string{"BASE_URL=https://x.trycloudflare.com"})
	assert.Equal(t, "https://x.trycloudflare.com", injected(pm, "BASE_URL"))
	assert.Equal(t, "false", injected(pm, "STRIPE_MOCK"))

	// Clearing the tunnel keeps the Stripe layer (the bug the layers fix).
	e.setTunnel(nil)
	assert.Equal(t, "http://localhost:3000", injected(pm, "BASE_URL"))
	assert.Equal(t, "false", injected(pm, "STRIPE_MOCK"))
}

func TestWebhookEndpointTarget(t *testing.T) {
	ep := WebhookEndpoint{URL: "u", ThinURL: "t"}
	assert.Equal(t, "u", ep.target(false, true), "connect falls back to plain")
	assert.Equal(t, "t", ep.target(true, true), "thin connect falls back to thin")
	ep.ConnectURL, ep.ThinConnectURL = "c", "tc"
	assert.Equal(t, "u", ep.target(false, false))
	assert.Equal(t, "c", ep.target(false, true))
	assert.Equal(t, "t", ep.target(true, false))
	assert.Equal(t, "tc", ep.target(true, true))
}

func TestStripeListenArgs(t *testing.T) {
	ep := WebhookEndpoint{URL: "http://localhost:8080/wh", ThinURL: "http://localhost:8080/wh/v2", ConnectURL: "http://localhost:9090/connect"}
	args := stripeListenArgs(ep, []string{"v2.core.account[requirements].updated", "v2.a"})
	assert.Equal(t, []string{"stripe", "listen", "--skip-update",
		"--forward-to", "http://localhost:8080/wh",
		"--forward-connect-to", "http://localhost:9090/connect",
		"--forward-thin-to", "http://localhost:8080/wh/v2",
		"--forward-thin-connect-to", "http://localhost:8080/wh/v2",
		"--thin-events", "v2.core.account[requirements].updated,v2.a",
	}, args)

	ep.ThinURL = ""
	assert.NotContains(t, stripeListenArgs(ep, []string{"v2.a"}), "--thin-events")
}

func TestCheckListenKey(t *testing.T) {
	assert.ErrorContains(t, checkListenKey(""), "not set")
	assert.ErrorContains(t, checkListenKey("sk_live_abc"), "live key")
	assert.ErrorContains(t, checkListenKey("rk_live_abc"), "live key")
	assert.NoError(t, checkListenKey("sk_test_abc"))
}

// TestStripeSwitch drives boot → flip → failed flip against a fake `stripe`
// on PATH that prints a ready line and records its args and key.
func TestStripeSwitch(t *testing.T) {
	dir := t.TempDir()
	record := filepath.Join(dir, "record")
	script := "#!/bin/sh\necho \"$STRIPE_API_KEY $*\" > " + record + "\n" +
		"echo 'Ready! Your webhook signing secret is whsec_fake123 (^C to quit)'\nexec sleep 60\n"
	require.NoError(t, os.WriteFile(filepath.Join(dir, "stripe"), []byte(script), 0o755))
	t.Setenv("PATH", dir+string(os.PathListSeparator)+os.Getenv("PATH"))
	dotenv := filepath.Join(dir, ".env")
	require.NoError(t, os.WriteFile(dotenv, []byte("STRIPE_KEY=sk_test_abc\n"), 0o600))

	ctx, cancel := context.WithCancel(context.Background())
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	cfg := &Config{}
	cfg.Dev.Stripe = StripeConfig{Mode: StripeModeListen, ThinEvents: []string{"v2.a"}}
	pm := NewProcessManager(logger)
	ep := WebhookEndpoint{URL: "http://localhost:8080/wh", Secret: "whsec_mock"}
	mock := NewStripeMock(StripeMockOptions{BaseURL: "http://localhost:3000"})
	mock.SetWebhookEndpoint(ep)
	s := &stripeSwitch{
		ctx: ctx, cfg: cfg, ep: ep, mock: mock, pm: pm,
		env:    &envLayers{ctx: ctx, cfg: cfg, pm: pm, logger: logger},
		logger: logger, broker: NewSSEBroker(nil, nil, nil, false, false, true, false, false), dotenv: dotenv,
	}
	t.Cleanup(func() {
		cancel()
		s.Close()
	})

	s.boot()
	require.Equal(t, StripeModeListen, s.Mode())
	assert.Equal(t, "whsec_fake123", injected(pm, "STRIPE_WEBHOOK_SECRET"))
	assert.Equal(t, "whsec_fake123", injected(pm, "STRIPE_WEBHOOK_SECRET_V2_CONNECT"))
	assert.Equal(t, "false", injected(pm, "STRIPE_MOCK"))
	assert.Equal(t, "sk_test_abc", injected(pm, "STRIPE_KEY"))
	assert.True(t, mock.listening.Load())
	rec, err := os.ReadFile(record)
	require.NoError(t, err)
	assert.Equal(t, "sk_test_abc listen --skip-update --forward-to http://localhost:8080/wh --forward-connect-to http://localhost:8080/wh\n", string(rec), "key via env, no thin forwards without a thin URL")

	// A .env edit drops the injected key at once, keeping the listener's secret,
	// so the site rule's own .env restart hands the app its fresh .env key.
	s.dropKey()
	assert.Empty(t, injected(pm, "STRIPE_KEY"))
	assert.Equal(t, "whsec_fake123", injected(pm, "STRIPE_WEBHOOK_SECRET"))
	assert.Equal(t, "false", injected(pm, "STRIPE_MOCK"))
	// An edit that left STRIPE_KEY alone restores it without a new listener.
	s.dotenvChanged()
	assert.Equal(t, "sk_test_abc", injected(pm, "STRIPE_KEY"))
	assert.Equal(t, "whsec_fake123", injected(pm, "STRIPE_WEBHOOK_SECRET"))

	// Mock webhooks refuse while listening.
	assert.ErrorIs(t, mock.FireEvent(ctx, "charge.refunded", map[string]any{"id": "ch_1"}), errStripeListening)

	mode, err := s.Set("")
	require.NoError(t, err)
	assert.Equal(t, StripeModeMock, mode)
	assert.Equal(t, "whsec_mock", injected(pm, "STRIPE_WEBHOOK_SECRET"))
	assert.Equal(t, "true", injected(pm, "STRIPE_MOCK"))
	assert.Empty(t, injected(pm, "STRIPE_KEY"), "mock mode leaves the key to .env")
	assert.False(t, mock.listening.Load())

	// A live key is refused and the mock stays up.
	require.NoError(t, os.WriteFile(dotenv, []byte("STRIPE_KEY=sk_live_abc\n"), 0o600))
	mode, err = s.Set(StripeModeListen)
	assert.ErrorContains(t, err, "live key")
	assert.Equal(t, StripeModeMock, mode)
	assert.Equal(t, "whsec_mock", injected(pm, "STRIPE_WEBHOOK_SECRET"))
}
