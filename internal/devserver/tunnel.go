package devserver

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"net"
	"net/http"
	"os/exec"
	"path"
	"regexp"
	"strconv"
	"strings"
	"sync"
	"syscall"
	"time"
)

// TunnelConfig holds the [dev.tunnel] table. The tunnel is never started at
// boot: the TUI's T hotkey toggles it at runtime. While on, hamr dev runs a
// locally installed tunnel binary pointed at a dedicated proxy listener and
// sets each var in Env to the public URL for the processes it spawns.
type TunnelConfig struct {
	// Provider picks the built-in command: "cloudflared" (default, no account,
	// random *.trycloudflare.com URL per start) or "ngrok".
	Provider string `toml:"provider"`
	// Args are extra flags appended to the built-in provider command.
	Args []string `toml:"args"`
	// Cmd replaces Provider+Args with a custom shell command. {port} is
	// replaced with the tunnel listener's port; the first https:// URL the
	// command prints is taken as the public URL.
	Cmd string `toml:"cmd"`
	// Env lists the vars set to the public URL. Default ["BASE_URL"].
	Env []string `toml:"env"`
}

const (
	TunnelCloudflared = "cloudflared"
	TunnelNgrok       = "ngrok"
)

// ResolvedProvider returns Provider with the default applied.
func (c TunnelConfig) ResolvedProvider() string {
	if c.Provider == "" {
		return TunnelCloudflared
	}
	return c.Provider
}

// ResolvedEnv returns Env with the default applied. An explicit empty list
// means "set nothing".
func (c TunnelConfig) ResolvedEnv() []string {
	if c.Env == nil {
		return []string{"BASE_URL"}
	}
	return c.Env
}

func (c TunnelConfig) validate() error {
	if c.Cmd != "" {
		if c.Provider != "" || len(c.Args) > 0 {
			return fmt.Errorf("[dev.tunnel] cmd replaces provider and args: set cmd, or provider/args, not both")
		}
		if !strings.Contains(c.Cmd, "{port}") {
			return fmt.Errorf("[dev.tunnel] cmd must contain {port} (replaced with the local port the tunnel should forward to)")
		}
		return nil
	}
	switch c.ResolvedProvider() {
	case TunnelCloudflared, TunnelNgrok:
		return nil
	}
	return fmt.Errorf("[dev.tunnel] provider %q: must be %q or %q (or use cmd for anything else)", c.Provider, TunnelCloudflared, TunnelNgrok)
}

// Per-provider URL patterns. cloudflared's banner carries other https links
// (docs, terms) before the tunnel URL, and ngrok is run with logfmt output
// so the URL arrives as url=...; a custom cmd takes the first https URL.
var (
	cloudflaredURLRE = regexp.MustCompile(`(https://[a-z0-9-]+\.trycloudflare\.com)`)
	ngrokURLRE       = regexp.MustCompile(`\burl=(https://[^\s"]+)`)
	anyHTTPSURLRE    = regexp.MustCompile(`(https://[^\s"'<>]+)`)
)

// command returns the argv to run and the pattern that extracts the public
// URL from its output.
func (c TunnelConfig) command(port int) ([]string, *regexp.Regexp) {
	if c.Cmd != "" {
		return []string{"sh", "-c", strings.ReplaceAll(c.Cmd, "{port}", strconv.Itoa(port))}, anyHTTPSURLRE
	}
	addr := "127.0.0.1:" + strconv.Itoa(port)
	if c.ResolvedProvider() == TunnelNgrok {
		return append([]string{"ngrok", "http", addr, "--log", "stdout", "--log-format", "logfmt"}, c.Args...), ngrokURLRE
	}
	return append([]string{"cloudflared", "tunnel", "--no-autoupdate", "--url", "http://" + addr}, c.Args...), cloudflaredURLRE
}

// tunnelBlockedPrefixes are the proxy routes that run commands, leak the raw
// dev log, or let a visitor write into it (browser console ingest).
// Unreachable through the tunnel; the mocks stay reachable.
var tunnelBlockedPrefixes = []string{"/__hamr/rule", "/__hamr/docker", "/__hamr/mcp", "/__hamr/logs", "/__hamr/console"}

type tunnelCtxKey struct{}

// isTunnelRequest reports whether r arrived through the tunnel listener.
// The live-reload stream and the build error page use it to withhold process
// output from tunnel visitors.
func isTunnelRequest(r *http.Request) bool {
	return r.Context().Value(tunnelCtxKey{}) != nil
}

// blockDevCommands wraps the proxy handler for the tunnel listener, refusing
// the routes in tunnelBlockedPrefixes and marking the rest as tunnel requests.
// The tunnel gets its own listener so every request on it is tunnel traffic
// by construction — no Host sniffing.
func blockDevCommands(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		p := path.Clean(r.URL.Path)
		for _, prefix := range tunnelBlockedPrefixes {
			if p == prefix || strings.HasPrefix(p, prefix+"/") {
				http.Error(w, "not available through the hamr dev tunnel", http.StatusForbidden)
				return
			}
		}
		next.ServeHTTP(w, r.WithContext(context.WithValue(r.Context(), tunnelCtxKey{}, true)))
	})
}

// tunnelHiddenOutput replaces build output shown to tunnel visitors.
const tunnelHiddenOutput = "(build output is hidden through the hamr dev tunnel)"

// redactForTunnel returns evt as a tunnel visitor may see it: process output
// is dropped (ok=false) and build error output is replaced. Everything else
// passes through unchanged.
func redactForTunnel(evt SSEEvent) (SSEEvent, bool) {
	switch evt.Type {
	case EvOutput:
		return evt, false
	case EvBuildError:
		var payload struct {
			Rule string `json:"rule"`
		}
		_ = json.Unmarshal([]byte(evt.Data), &payload)
		return buildErrorEvent(payload.Rule, tunnelHiddenOutput), true
	}
	return evt, true
}

// tunnelURLTimeout bounds how long a toggle waits for the public URL.
const tunnelURLTimeout = 15 * time.Second

// tunnelProc is one running tunnel: its listener, server and child process.
type tunnelProc struct {
	srv  *http.Server
	cmd  *exec.Cmd
	done chan struct{} // closed when cmd.Wait returns
}

// tunnel owns the runtime on/off state behind the T hotkey.
type tunnel struct {
	ctx     context.Context
	cfg     *Config
	handler http.Handler // proxy handler already wrapped by blockDevCommands
	pm      *ProcessManager
	logger  *slog.Logger
	// baseEnv is the injected env without tunnel vars. Written by Run before
	// ready and only read after (handleHotkey gates on ready), same as proxyURL.
	baseEnv []string
	broker  *SSEBroker // tunnel_start / tunnel_up / tunnel_down go out here

	mu   sync.Mutex
	busy bool
	proc *tunnelProc
	url  string // public URL while on; "" when off
}

// URL returns the public URL, or "" when the tunnel is off.
func (t *tunnel) URL() string {
	t.mu.Lock()
	defer t.mu.Unlock()
	return t.url
}

// Toggle turns the tunnel on or off. Blocks until the URL is known (or the
// start fails), so callers run it on its own goroutine.
func (t *tunnel) Toggle() {
	t.mu.Lock()
	if t.busy {
		t.mu.Unlock()
		t.logger.Warn("tunnel is already starting or stopping")
		return
	}
	t.busy = true
	p := t.proc
	t.proc = nil
	t.mu.Unlock()
	defer func() {
		t.mu.Lock()
		t.busy = false
		t.mu.Unlock()
	}()

	if p != nil {
		t.broker.Broadcast(SSEEvent{Type: EvTunnelStart, Data: "stopping"})
		p.stop()
		t.logger.Info("tunnel stopped")
		t.apply("")
		return
	}
	t.broker.Broadcast(SSEEvent{Type: EvTunnelStart, Data: "starting"})
	p, url, err := t.start()
	if err != nil {
		t.logger.Error("tunnel failed to start", "err", err)
		t.broker.Broadcast(SSEEvent{Type: EvTunnelDown})
		return
	}
	t.mu.Lock()
	t.proc = p
	t.mu.Unlock()
	t.logger.Info("tunnel up", "url", url)
	t.apply(url)

	go func() {
		<-p.done
		t.mu.Lock()
		if t.proc != p {
			t.mu.Unlock()
			return // stopped on purpose; stop() closed the listener
		}
		t.proc = nil
		t.mu.Unlock()
		_ = p.srv.Close()
		if t.ctx.Err() != nil {
			return // shutting down
		}
		t.logger.Warn("tunnel process exited; tunnel is off")

		// Reset under the toggle guard, waiting out a toggle in flight. Once
		// it's ours: a T press may have started a new tunnel meanwhile
		// (proc set again), which owns the env — leave it alone.
		// ponytail: 50ms poll for the guard; a sync.Cond if this ever matters.
		for {
			t.mu.Lock()
			if !t.busy {
				break
			}
			t.mu.Unlock()
			time.Sleep(50 * time.Millisecond)
		}
		if t.proc != nil || t.ctx.Err() != nil {
			t.mu.Unlock()
			return
		}
		t.busy = true
		t.mu.Unlock()
		defer func() {
			t.mu.Lock()
			t.busy = false
			t.mu.Unlock()
		}()
		t.apply("")
	}()
}

// Close stops a running tunnel without touching env or processes. Shutdown path.
func (t *tunnel) Close() {
	t.mu.Lock()
	p := t.proc
	t.proc = nil
	t.mu.Unlock()
	if p != nil {
		p.stop()
	}
}

func (t *tunnel) start() (*tunnelProc, string, error) {
	tc := t.cfg.Dev.Tunnel
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		return nil, "", fmt.Errorf("bind tunnel listener: %w", err)
	}
	argv, urlRE := tc.command(ln.Addr().(*net.TCPAddr).Port)
	if _, err := exec.LookPath(argv[0]); err != nil {
		_ = ln.Close()
		return nil, "", fmt.Errorf("%s not found on PATH (install it, or set [dev.tunnel] cmd)", argv[0])
	}

	cmd := exec.CommandContext(t.ctx, argv[0], argv[1:]...)
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
	cmd.Cancel = func() error { return killGroup(cmd, syscall.SIGKILL) }
	cmd.WaitDelay = 2 * time.Second

	tail := newTailBuffer()
	found := make(chan string, 1)
	scan := &urlScanner{re: urlRE, found: found}
	stdoutDest, stderrDest := t.pm.prefixDests()
	color := nextColor()
	outs := []io.Writer{newPrefixWriter(stdoutDest, "tunnel", color), tail, scan}
	errs := []io.Writer{newPrefixWriter(stderrDest, "tunnel", color), tail, scan}
	if t.pm.logBuf != nil {
		lw := newLogWriter("tunnel", color, t.pm.logBuf, t.pm.logBroker)
		outs, errs = append(outs, lw), append(errs, lw)
	}
	cmd.Stdout = io.MultiWriter(outs...)
	cmd.Stderr = io.MultiWriter(errs...)

	srv := serveProxy(ln, t.handler)
	if err := cmd.Start(); err != nil {
		_ = srv.Close()
		return nil, "", fmt.Errorf("start %s: %w", argv[0], err)
	}
	p := &tunnelProc{srv: srv, cmd: cmd, done: make(chan struct{})}
	go func() {
		_ = cmd.Wait()
		close(p.done)
	}()

	timer := time.NewTimer(tunnelURLTimeout)
	defer timer.Stop()
	select {
	case url := <-found:
		return p, url, nil
	case <-p.done:
		_ = srv.Close()
		return nil, "", fmt.Errorf("%s exited before printing a URL; output:\n%s", argv[0], tail.String())
	case <-timer.C:
		p.stop()
		return nil, "", fmt.Errorf("no public URL from %s within %s; output:\n%s", argv[0], tunnelURLTimeout, tail.String())
	case <-t.ctx.Done():
		p.stop()
		return nil, "", t.ctx.Err()
	}
}

// stop signals the tunnel's process group, escalates to SIGKILL if it hangs,
// and closes the listener.
func (p *tunnelProc) stop() {
	_ = killGroup(p.cmd, syscall.SIGTERM)
	select {
	case <-p.done:
	case <-time.After(shutdownTimeout):
		_ = killGroup(p.cmd, syscall.SIGKILL)
		<-p.done
	}
	_ = p.srv.Close()
}

func killGroup(cmd *exec.Cmd, sig syscall.Signal) error {
	if pgid, err := syscall.Getpgid(cmd.Process.Pid); err == nil {
		return syscall.Kill(-pgid, sig)
	}
	return cmd.Process.Signal(sig)
}

// apply points the injected env at url ("" removes the tunnel vars),
// restarts the running run-rules and daemons so they pick it up, and
// broadcasts tunnel_up / tunnel_down once they're back. Builds are not re-run.
func (t *tunnel) apply(url string) {
	t.mu.Lock()
	t.url = url
	t.mu.Unlock()

	env := append([]string(nil), t.baseEnv...)
	if url != "" {
		// Last so they win buildEnv's last-wins over a .env rewrite of the same key.
		for _, k := range t.cfg.Dev.Tunnel.ResolvedEnv() {
			env = append(env, k+"="+url)
		}
	}
	t.pm.SetInjectedEnv(env)

	// Only restart what is running: a rule whose build failed stays down.
	// StartProcess is serialized, so a scheduler rebuild of the same rule
	// racing this just restarts it twice.
	for i := range t.cfg.Dev.Watch {
		rule := &t.cfg.Dev.Watch[i]
		if rule.Run != "" && t.pm.isRunning(rule.Name) {
			if err := t.pm.StartProcess(t.ctx, rule); err != nil {
				t.logger.Error("restart failed", "rule", rule.Name, "err", err)
			}
		}
	}
	for i := range t.cfg.Dev.Daemons {
		d := &t.cfg.Dev.Daemons[i]
		if t.pm.isRunning(d.Name) {
			if err := t.pm.StartProcess(t.ctx, &WatchRule{Name: d.Name, Run: d.Cmd, Dir: d.Dir, Env: d.Env}); err != nil {
				t.logger.Error("restart failed", "daemon", d.Name, "err", err)
			}
		}
	}
	if url != "" {
		t.broker.Broadcast(SSEEvent{Type: EvTunnelUp, Data: url})
	} else {
		t.broker.Broadcast(SSEEvent{Type: EvTunnelDown})
	}
}

// urlScanner watches process output line by line and sends the first URL
// matching re on found. Output after the first match is ignored.
type urlScanner struct {
	re    *regexp.Regexp
	found chan<- string
	mu    sync.Mutex
	buf   []byte
	done  bool
}

func (s *urlScanner) Write(p []byte) (int, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.done {
		return len(p), nil
	}
	s.buf = append(s.buf, p...)
	for {
		i := bytes.IndexByte(s.buf, '\n')
		if i < 0 {
			break
		}
		line := s.buf[:i]
		s.buf = s.buf[i+1:]
		if m := s.re.FindSubmatch(line); m != nil {
			s.done = true
			s.buf = nil
			s.found <- strings.TrimRight(string(m[1]), ".,;)")
			break
		}
	}
	return len(p), nil
}
