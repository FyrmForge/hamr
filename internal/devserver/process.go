package devserver

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"log/slog"
	"os"
	"os/exec"
	"strings"
	"sync"
	"syscall"
	"time"
)

const shutdownTimeout = 5 * time.Second

// tailBuffer is a fixed-size ring buffer implementing io.Writer.
// It keeps only the last tailBufSize bytes written, discarding older data.
type tailBuffer struct {
	mu   sync.Mutex
	buf  []byte
	size int
	pos  int // next write position in the ring
	full bool
}

const tailBufSize = 8192

func newTailBuffer() *tailBuffer {
	return &tailBuffer{buf: make([]byte, tailBufSize), size: tailBufSize}
}

func (tb *tailBuffer) Write(p []byte) (int, error) {
	tb.mu.Lock()
	defer tb.mu.Unlock()
	n := len(p)
	if n >= tb.size {
		// Data larger than buffer — keep only the last size bytes.
		copy(tb.buf, p[n-tb.size:])
		tb.pos = 0
		tb.full = true
		return n, nil
	}
	// How much fits before wrapping?
	space := tb.size - tb.pos
	if n <= space {
		copy(tb.buf[tb.pos:], p)
		tb.pos += n
		if tb.pos == tb.size {
			tb.pos = 0
			tb.full = true
		}
	} else {
		copy(tb.buf[tb.pos:], p[:space])
		copy(tb.buf, p[space:])
		tb.pos = n - space
		tb.full = true
	}
	return n, nil
}

func (tb *tailBuffer) String() string {
	tb.mu.Lock()
	defer tb.mu.Unlock()
	if !tb.full {
		return string(tb.buf[:tb.pos])
	}
	// Ring has wrapped — [pos:] + [:pos].
	out := make([]byte, tb.size)
	n := copy(out, tb.buf[tb.pos:])
	copy(out[n:], tb.buf[:tb.pos])
	return string(out)
}

func (tb *tailBuffer) Reset() {
	tb.mu.Lock()
	defer tb.mu.Unlock()
	tb.pos = 0
	tb.full = false
}

var _ io.Writer = (*tailBuffer)(nil)

// ProcessManager handles running one-shot commands and long-running processes.
type ProcessManager struct {
	mu            sync.Mutex
	procs         map[string]*os.Process
	logger        *slog.Logger
	OnProcessExit func(rule string, err error, output string)
	logBuf        *LogBuffer
	logBroker     *SSEBroker
	fileLog       io.Writer // rolling file logger (nil if disabled)
	stdinR        *os.File  // read end of pipe, given to child processes
	stdinW        *os.File  // write end, kept open to prevent EOF
}

// SetLogOutput enables streaming process output to a LogBuffer and SSE broker.
func (pm *ProcessManager) SetLogOutput(buf *LogBuffer, broker *SSEBroker) {
	pm.logBuf = buf
	pm.logBroker = broker
}

// SetFileLog enables writing prefixed process output to a rolling file logger.
func (pm *ProcessManager) SetFileLog(w io.Writer) {
	pm.fileLog = w
}

// NewProcessManager creates a new process manager.
func NewProcessManager(logger *slog.Logger) *ProcessManager {
	stdinR, stdinW, _ := os.Pipe()
	return &ProcessManager{
		procs:  make(map[string]*os.Process),
		logger: logger,
		stdinR: stdinR,
		stdinW: stdinW,
	}
}

// RunCommand runs a one-shot command to completion.
// Stdout and stderr are streamed through the logger.
// On failure the captured tail output is returned alongside the error.
func (pm *ProcessManager) RunCommand(ctx context.Context, rule *WatchRule) (string, error) {
	pm.logger.Info("running", "rule", rule.Name, "cmd", rule.Cmd)

	cmd := exec.CommandContext(ctx, "sh", "-c", rule.Cmd)
	cmd.Env = buildEnv(rule.Env)
	color := nextColor()
	capture := newTailBuffer()

	// Show [name:build] when the rule also has a run step, plain [name] otherwise.
	displayName := rule.Name
	if rule.Run != "" {
		displayName = rule.Name + ":build"
	}

	stdoutDest, stderrDest := pm.prefixDests()
	var lw *logWriter
	if pm.logBuf != nil {
		lw = newLogWriter(displayName, color, pm.logBuf, pm.logBroker)
		cmd.Stdout = io.MultiWriter(newPrefixWriter(stdoutDest, displayName, color), capture, lw)
		cmd.Stderr = io.MultiWriter(newPrefixWriter(stderrDest, displayName, color), capture, lw)
	} else {
		cmd.Stdout = io.MultiWriter(newPrefixWriter(stdoutDest, displayName, color), capture)
		cmd.Stderr = io.MultiWriter(newPrefixWriter(stderrDest, displayName, color), capture)
	}

	if err := cmd.Run(); err != nil {
		if lw != nil {
			lw.Flush()
		}
		return capture.String(), fmt.Errorf("rule %q cmd failed: %w", rule.Name, err)
	}
	if lw != nil {
		lw.Flush()
	}
	return "", nil
}

// StartProcess starts a long-running process, killing any previous instance.
// The process is tracked and can be stopped via StopAll.
func (pm *ProcessManager) StartProcess(ctx context.Context, rule *WatchRule) error {
	pm.stopProcess(rule.Name)

	pm.logger.Info("starting", "rule", rule.Name, "run", rule.Run)

	cmd := exec.CommandContext(ctx, "sh", "-c", rule.Run)
	cmd.Env = buildEnv(rule.Env)
	cmd.Stdin = pm.stdinR
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
	color := nextColor()
	capture := newTailBuffer()

	// Show [name:run] when the rule also has a build step, plain [name] otherwise.
	displayName := rule.Name
	if rule.Cmd != "" {
		displayName = rule.Name + ":run"
	}

	stdoutDest, stderrDest := pm.prefixDests()
	var lw *logWriter
	if pm.logBuf != nil {
		lw = newLogWriter(displayName, color, pm.logBuf, pm.logBroker)
		cmd.Stdout = io.MultiWriter(newPrefixWriter(stdoutDest, displayName, color), capture, lw)
		cmd.Stderr = io.MultiWriter(newPrefixWriter(stderrDest, displayName, color), capture, lw)
	} else {
		cmd.Stdout = io.MultiWriter(newPrefixWriter(stdoutDest, displayName, color), capture)
		cmd.Stderr = io.MultiWriter(newPrefixWriter(stderrDest, displayName, color), capture)
	}

	if err := cmd.Start(); err != nil {
		return fmt.Errorf("rule %q start failed: %w", rule.Name, err)
	}

	pm.mu.Lock()
	pm.procs[rule.Name] = cmd.Process
	pm.mu.Unlock()

	// Wait in background so we can clean up the map entry.
	ruleName := rule.Name
	go func() {
		err := cmd.Wait()
		if lw != nil {
			lw.Flush()
		}
		pm.mu.Lock()
		tracked := pm.procs[ruleName] == cmd.Process
		if tracked {
			delete(pm.procs, ruleName)
		}
		cb := pm.OnProcessExit
		pm.mu.Unlock()
		if err != nil {
			pm.logger.Warn("process exited", "rule", ruleName, "err", err)
		}
		// Only invoke callback if the process was still tracked (not intentionally stopped).
		if tracked && cb != nil && err != nil {
			cb(ruleName, err, capture.String())
		}
	}()

	return nil
}

// ClearCallbacks disables all process exit callbacks.
// Used during shutdown to prevent spurious build_error events.
func (pm *ProcessManager) ClearCallbacks() {
	pm.mu.Lock()
	pm.OnProcessExit = nil
	pm.mu.Unlock()
}

// StopAll gracefully stops all tracked processes.
func (pm *ProcessManager) StopAll() {
	pm.mu.Lock()
	names := make([]string, 0, len(pm.procs))
	for name := range pm.procs {
		names = append(names, name)
	}
	pm.mu.Unlock()

	for _, name := range names {
		pm.stopProcess(name)
	}
}

func (pm *ProcessManager) stopProcess(name string) {
	pm.mu.Lock()
	proc, ok := pm.procs[name]
	if !ok {
		pm.mu.Unlock()
		return
	}
	delete(pm.procs, name)
	pm.mu.Unlock()

	pm.logger.Info("stopping", "rule", name)

	// Send SIGINT to the process group.
	pgid, err := syscall.Getpgid(proc.Pid)
	if err == nil {
		_ = syscall.Kill(-pgid, syscall.SIGINT)
	} else {
		_ = proc.Signal(syscall.SIGINT)
	}

	// Wait for graceful shutdown.
	done := make(chan struct{})
	go func() {
		_, _ = proc.Wait()
		close(done)
	}()

	select {
	case <-done:
	case <-time.After(shutdownTimeout):
		pm.logger.Warn("force killing", "rule", name)
		if pgid, err := syscall.Getpgid(proc.Pid); err == nil {
			_ = syscall.Kill(-pgid, syscall.SIGKILL)
		} else {
			_ = proc.Kill()
		}
		<-done
	}
}

// buildEnv merges rule env vars with the current environment.
// Rule vars override existing values (last-wins).
func buildEnv(ruleEnv []string) []string {
	if len(ruleEnv) == 0 {
		return nil // inherit parent env
	}

	env := os.Environ()
	// Build a map for dedup (last-wins).
	envMap := make(map[string]string, len(env)+len(ruleEnv))
	for _, e := range env {
		if k, _, ok := strings.Cut(e, "="); ok {
			envMap[k] = e
		}
	}
	for _, e := range ruleEnv {
		if k, _, ok := strings.Cut(e, "="); ok {
			envMap[k] = e
		}
	}

	result := make([]string, 0, len(envMap))
	for _, v := range envMap {
		result = append(result, v)
	}
	return result
}

// prefixDests returns the stdout and stderr destinations for prefixWriters,
// wrapping with the file logger if configured.
func (pm *ProcessManager) prefixDests() (stdout, stderr io.Writer) {
	if pm.fileLog != nil {
		return io.MultiWriter(os.Stdout, pm.fileLog), io.MultiWriter(os.Stderr, pm.fileLog)
	}
	return os.Stdout, os.Stderr
}

// prefixWriter implements io.Writer, prepending a colored tag to each line
// while passing through the raw bytes (preserving ANSI colors from child
// processes).
type prefixWriter struct {
	dest   io.Writer
	tag    []byte // e.g. "\033[36m[site:build]\033[0m "
	buf    []byte
	mu     sync.Mutex
}

var ruleColors = [...]string{
	"\033[36m", // cyan
	"\033[33m", // yellow
	"\033[35m", // magenta
	"\033[32m", // green
	"\033[34m", // blue
}

const colorReset = "\033[0m"

var colorIndex int
var colorMu sync.Mutex

func nextColor() string {
	colorMu.Lock()
	c := ruleColors[colorIndex%len(ruleColors)]
	colorIndex++
	colorMu.Unlock()
	return c
}

func newPrefixWriter(dest io.Writer, name, color string) *prefixWriter {
	tag := []byte(color + "[" + name + "]" + colorReset + " ")
	return &prefixWriter{dest: dest, tag: tag}
}

// termMu serializes writes to the terminal across all output sources
// (child process output, dev server logs, status bar) to prevent
// interleaving and visual corruption.
var termMu sync.Mutex

func (w *prefixWriter) Write(p []byte) (int, error) {
	w.mu.Lock()
	defer w.mu.Unlock()

	w.buf = append(w.buf, p...)

	// Fast path: no complete lines yet.
	if bytes.IndexByte(w.buf, '\n') < 0 {
		return len(p), nil
	}

	// Batch all complete lines into one write.
	var out []byte
	for {
		idx := bytes.IndexByte(w.buf, '\n')
		if idx < 0 {
			break
		}
		line := w.buf[:idx]
		w.buf = w.buf[idx+1:]
		out = append(out, w.tag...)
		out = append(out, line...)
		out = append(out, '\r', '\n')
	}

	termMu.Lock()
	_, _ = w.dest.Write(out)
	termMu.Unlock()

	return len(p), nil
}

var _ io.Writer = (*prefixWriter)(nil)
