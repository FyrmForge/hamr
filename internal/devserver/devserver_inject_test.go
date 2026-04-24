package devserver

import (
	"testing"

	"github.com/stretchr/testify/assert"
)

// TestBuildHamrInjectedEnv covers the env-var injection table — the
// vars hamr dev sets on every spawned rule process so scaffolded sites
// can discover hamr-served URLs without hardcoding ports.
//
// The contract: HAMR_DEV_URL fires when EITHER mock is enabled (both use
// the proxy origin); HAMR_STRIPE_MOCK_URL fires only when stripe is
// enabled. Both derive from [proxy].listen so port changes are picked up
// without scaffold edits.
func TestBuildHamrInjectedEnv(t *testing.T) {
	t.Run("nothing enabled returns nil (inherit parent env)", func(t *testing.T) {
		cfg := &Config{ProxyConfigured: true}
		cfg.Proxy.Listen = ":3000"
		assert.Nil(t, buildHamrInjectedEnv(cfg))
	})

	t.Run("proxy not configured returns nil (mocks can't run without proxy)", func(t *testing.T) {
		cfg := &Config{ProxyConfigured: false}
		cfg.Dev.Stripe.Enabled = true
		cfg.Dev.Email.Enabled = true
		assert.Nil(t, buildHamrInjectedEnv(cfg))
	})

	t.Run("email mock only", func(t *testing.T) {
		cfg := &Config{ProxyConfigured: true}
		cfg.Proxy.Listen = ":3000"
		cfg.Dev.Email.Enabled = true
		got := buildHamrInjectedEnv(cfg)
		assert.Equal(t, []string{"HAMR_DEV_URL=http://localhost:3000"}, got)
	})

	t.Run("stripe mock only", func(t *testing.T) {
		cfg := &Config{ProxyConfigured: true}
		cfg.Proxy.Listen = ":3000"
		cfg.Dev.Stripe.Enabled = true
		got := buildHamrInjectedEnv(cfg)
		assert.Equal(t, []string{
			"HAMR_DEV_URL=http://localhost:3000",
			"HAMR_STRIPE_MOCK_URL=http://localhost:3000",
		}, got)
	})

	t.Run("both mocks enabled, custom port", func(t *testing.T) {
		cfg := &Config{ProxyConfigured: true}
		cfg.Proxy.Listen = ":3458"
		cfg.Dev.Email.Enabled = true
		cfg.Dev.Stripe.Enabled = true
		got := buildHamrInjectedEnv(cfg)
		assert.Equal(t, []string{
			"HAMR_DEV_URL=http://localhost:3458",
			"HAMR_STRIPE_MOCK_URL=http://localhost:3458",
		}, got, "non-default proxy port must propagate so scaffold doesn't need a hardcoded URL")
	})
}

// TestProcessManager_SetInjectedEnv covers the PM-side mechanism: vars
// set via SetInjectedEnv land in the spawned process's env, but rule.Env
// still wins on key conflicts (last-write-wins via buildEnv).
func TestProcessManager_SetInjectedEnv(t *testing.T) {
	pm := NewProcessManager(nil)
	pm.SetInjectedEnv([]string{"HAMR_INJECT=hamr_value", "OVERRIDABLE=hamr_value"})

	// buildEnv combines os.Environ + the slice we pass. Simulate the call
	// site in RunCommand/StartProcess: prepend pm.injectedEnv before rule.Env.
	combined := append(append([]string(nil), pm.injectedEnv...), "OVERRIDABLE=rule_value")
	got := buildEnv(combined)

	// Convert back to a map for assertions (buildEnv returns os.Environ-style slice).
	envMap := map[string]string{}
	for _, e := range got {
		k, v, _ := splitEnv(e)
		envMap[k] = v
	}
	assert.Equal(t, "hamr_value", envMap["HAMR_INJECT"], "injected var must reach the spawned process env")
	assert.Equal(t, "rule_value", envMap["OVERRIDABLE"], "rule.Env must win over injected on key conflict")
}

func splitEnv(s string) (k, v string, ok bool) {
	for i := 0; i < len(s); i++ {
		if s[i] == '=' {
			return s[:i], s[i+1:], true
		}
	}
	return s, "", false
}
