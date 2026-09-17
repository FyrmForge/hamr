package devserver

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/fsnotify/fsnotify"
)

// stripeListenTimeout bounds how long a switch waits for `stripe listen` to
// print its signing secret. It has to reach Stripe first.
const stripeListenTimeout = 30 * time.Second

// stripeSecretRE matches the secret on `stripe listen`'s ready line.
var stripeSecretRE = regexp.MustCompile(`(whsec_[A-Za-z0-9]+)`)

// stripeSecretEnv are the signing-secret vars injected in both modes. Locally
// one secret signs every kind of event, so they all hold the same value.
var stripeSecretEnv = []string{
	"STRIPE_WEBHOOK_SECRET",
	"STRIPE_WEBHOOK_SECRET_V2",
	"STRIPE_WEBHOOK_SECRET_CONNECT",
	"STRIPE_WEBHOOK_SECRET_V2_CONNECT",
}

// stripeEnvLayer is the env the Stripe layer injects for mode. STRIPE_KEY is
// injected in listen mode only, so the app uses the key the listener runs
// with; the mock ignores keys, and an injected one would go stale over the
// app's own .env on the next edit. An empty key leaves it out, so the app
// reads its own .env (see dropKey).
func stripeEnvLayer(mode, secret, key string) []string {
	env := []string{"STRIPE_MOCK=" + strconv.FormatBool(mode == StripeModeMock)}
	if mode == StripeModeListen && key != "" {
		env = append(env, "STRIPE_KEY="+key)
	}
	for _, k := range stripeSecretEnv {
		env = append(env, k+"="+secret)
	}
	return env
}

// stripeListenArgs builds the `stripe listen` argv. Always argv, never
// `sh -c`: thin event names carry [ ] the shell would glob. The thin
// forwards are left out when there is no thin URL to send them to.
func stripeListenArgs(ep WebhookEndpoint, thinEvents []string) []string {
	args := []string{"stripe", "listen", "--skip-update",
		"--forward-to", ep.URL,
		"--forward-connect-to", ep.target(false, true),
	}
	if ep.ThinURL != "" && len(thinEvents) > 0 {
		args = append(args,
			"--forward-thin-to", ep.ThinURL,
			"--forward-thin-connect-to", ep.target(true, true),
			"--thin-events", strings.Join(thinEvents, ","),
		)
	}
	return args
}

// readStripeKey returns STRIPE_KEY from the .env at path, falling back to
// hamr's own environment.
func readStripeKey(path string) string {
	if v, ok := ReadDotenvKey(path, "STRIPE_KEY"); ok {
		return v
	}
	return os.Getenv("STRIPE_KEY")
}

// checkListenKey refuses keys listen mode must not run with.
func checkListenKey(key string) error {
	switch {
	case key == "":
		return errors.New("STRIPE_KEY is not set in .env")
	case strings.HasPrefix(key, "sk_live_"), strings.HasPrefix(key, "rk_live_"):
		return errors.New("STRIPE_KEY is a live key; listen mode only runs against a sandbox")
	}
	return nil
}

// stripeSwitch owns the runtime Stripe mode behind the S hotkey and the
// stripe.mode MCP tool: the mock, or `stripe listen` against a real sandbox.
// Every switch sets the Stripe env layer and restarts the running apps.
type stripeSwitch struct {
	ctx    context.Context
	cfg    *Config
	ep     WebhookEndpoint // port-walked forward URLs; Secret is the mock's
	mock   *StripeMock
	pm     *ProcessManager
	env    *envLayers
	logger *slog.Logger
	broker *SSEBroker // stripe_mode goes out here
	dotenv string     // path of the .env STRIPE_KEY is read from

	// switchMu serializes switches. User switches TryLock and refuse while one
	// is in flight; the process-exit and .env paths wait their turn.
	switchMu sync.Mutex

	mu   sync.Mutex
	mode   string // StripeModeMock / StripeModeListen; "" until boot
	proc   *childProc
	key    string // STRIPE_KEY the current layer was built with
	secret string // signing secret the current layer was built with
}

// Mode returns the running mode, or "" before boot.
func (s *stripeSwitch) Mode() string {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.mode
}

// boot applies the configured mode before the first build (no restart) and
// starts the .env watch. A listener that won't start falls back to the mock.
func (s *stripeSwitch) boot() {
	s.switchMu.Lock()
	defer s.switchMu.Unlock()
	if err := s.switchTo(s.cfg.Dev.Stripe.ResolvedMode(), false); err != nil {
		s.logger.Warn("stripe listen failed to start; running the mock instead (S retries)", "err", err)
	}
	go s.watchDotenv()
}

// Set switches to mode ("" flips mock ⇄ listen) and returns the mode running
// afterwards. Blocks until the switch is done.
func (s *stripeSwitch) Set(mode string) (string, error) {
	if !s.switchMu.TryLock() {
		err := errors.New("stripe is already switching")
		s.logger.Warn(err.Error())
		return s.Mode(), err
	}
	defer s.switchMu.Unlock()
	cur := s.Mode()
	if mode == "" {
		mode = StripeModeListen
		if cur == StripeModeListen {
			mode = StripeModeMock
		}
	}
	if mode != StripeModeMock && mode != StripeModeListen {
		return cur, fmt.Errorf("stripe mode %q: must be %q or %q", mode, StripeModeMock, StripeModeListen)
	}
	if mode == cur {
		return cur, nil
	}
	err := s.switchTo(mode, true)
	if err != nil {
		s.logger.Error("stripe switch failed", "to", mode, "err", err)
	}
	return s.Mode(), err
}

// switchTo runs target and points the env layer at it. Caller holds
// switchMu. A running listener is always stopped first; if the new one
// fails, the app falls back to the mock unless it was already on it — never
// left on listen env with no listener.
func (s *stripeSwitch) switchTo(target string, restart bool) error {
	s.broker.Broadcast(SSEEvent{Type: EvStripeMode, Data: "switching"})
	s.mu.Lock()
	prev, old := s.mode, s.proc
	s.proc = nil
	s.mu.Unlock()
	if old != nil {
		old.stop()
	}

	key := readStripeKey(s.dotenv)
	secret := s.ep.Secret
	var p *childProc
	var err error
	if target == StripeModeListen {
		if err = checkListenKey(key); err == nil {
			argv := stripeListenArgs(s.ep, s.cfg.Dev.Stripe.ResolvedThinEvents())
			if _, lerr := exec.LookPath(argv[0]); lerr != nil {
				err = errors.New("stripe CLI not found on PATH (install it: https://docs.stripe.com/stripe-cli)")
			} else {
				// Key via env, not --api-key, so it doesn't show in ps.
				env := append(os.Environ(), "STRIPE_API_KEY="+key)
				p, secret, err = startChild(s.ctx, s.pm, "stripe", argv, env, stripeSecretRE, "a signing secret", stripeListenTimeout)
			}
		}
		if err != nil {
			if prev == StripeModeMock && old == nil {
				s.broker.Broadcast(SSEEvent{Type: EvStripeMode, Data: prev})
				return err
			}
			target, secret = StripeModeMock, s.ep.Secret
		}
	}

	s.mu.Lock()
	s.mode, s.proc, s.key, s.secret = target, p, key, secret
	s.mu.Unlock()
	s.mock.listening.Store(target == StripeModeListen)
	s.env.setStripe(stripeEnvLayer(target, secret, key), restart)
	s.logger.Info("stripe mode", "mode", target)
	s.broker.Broadcast(SSEEvent{Type: EvStripeMode, Data: target})
	if p != nil {
		go s.watchExit(p)
	}
	return err
}

// watchExit falls back to the mock when p exits on its own.
func (s *stripeSwitch) watchExit(p *childProc) {
	<-p.done
	s.switchMu.Lock()
	defer s.switchMu.Unlock()
	s.mu.Lock()
	ours := s.proc == p
	s.mu.Unlock()
	if !ours || s.ctx.Err() != nil {
		return // stopped on purpose, or shutting down
	}
	s.logger.Warn("stripe listen exited; switching to the mock")
	_ = s.switchTo(StripeModeMock, true)
}

// watchDotenv restarts the listener when STRIPE_KEY in .env changes in listen
// mode. Runs in both modes (S can flip to listen) until ctx ends.
func (s *stripeSwitch) watchDotenv() {
	abs, err := filepath.Abs(s.dotenv)
	if err != nil {
		s.logger.Warn("stripe: can't watch .env", "err", err)
		return
	}
	fsw, err := fsnotify.NewWatcher()
	if err != nil {
		s.logger.Warn("stripe: can't watch .env", "err", err)
		return
	}
	defer func() { _ = fsw.Close() }()
	// The directory, not the file: editors replace .env by rename.
	if err := fsw.Add(filepath.Dir(abs)); err != nil {
		s.logger.Warn("stripe: can't watch .env", "err", err)
		return
	}
	var debounce <-chan time.Time
	for {
		select {
		case <-s.ctx.Done():
			return
		case ev, ok := <-fsw.Events:
			if !ok {
				return
			}
			if ev.Name == abs {
				s.dropKey()
				debounce = time.After(300 * time.Millisecond)
			}
		case _, ok := <-fsw.Errors:
			if !ok {
				return
			}
		case <-debounce:
			debounce = nil
			s.dotenvChanged()
		}
	}
}

// dropKey takes the injected STRIPE_KEY out of the listen-mode layer as soon
// as .env changes, without a restart. The site rule's own .env watch restarts
// the app before dotenvChanged has a new listener up; without this, that
// restart would hand the app the old key. dotenvChanged puts the key back:
// at once when it didn't change, with the new listener's secret when it did.
func (s *stripeSwitch) dropKey() {
	s.mu.Lock()
	listen, secret := s.mode == StripeModeListen, s.secret
	s.mu.Unlock()
	if listen {
		s.env.setStripe(stripeEnvLayer(StripeModeListen, secret, ""), false)
	}
}

func (s *stripeSwitch) dotenvChanged() {
	s.switchMu.Lock()
	defer s.switchMu.Unlock()
	key := readStripeKey(s.dotenv)
	s.mu.Lock()
	listen, same, secret := s.mode == StripeModeListen, key == s.key, s.secret
	s.mu.Unlock()
	if !listen || s.ctx.Err() != nil {
		return
	}
	if same {
		// Some other line changed: put back the key dropKey took out. No
		// restart; the site rule's own .env restart picks it up.
		s.env.setStripe(stripeEnvLayer(StripeModeListen, secret, key), false)
		return
	}
	s.logger.Info("STRIPE_KEY changed in .env; restarting stripe listen")
	if err := s.switchTo(StripeModeListen, true); err != nil {
		s.logger.Error("stripe listen failed to restart; switched to the mock", "err", err)
	}
}

// Close stops a running listener without touching env or processes. Shutdown path.
func (s *stripeSwitch) Close() {
	s.mu.Lock()
	p := s.proc
	s.proc = nil
	s.mu.Unlock()
	if p != nil {
		p.stop()
	}
}
