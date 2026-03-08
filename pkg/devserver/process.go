package devserver

import (
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

// ProcessManager handles running one-shot commands and long-running processes.
type ProcessManager struct {
	mu     sync.Mutex
	procs  map[string]*os.Process
	logger *slog.Logger
}

// NewProcessManager creates a new process manager.
func NewProcessManager(logger *slog.Logger) *ProcessManager {
	return &ProcessManager{
		procs:  make(map[string]*os.Process),
		logger: logger,
	}
}

// RunCommand runs a one-shot command to completion.
// Stdout and stderr are streamed through the logger.
func (pm *ProcessManager) RunCommand(ctx context.Context, rule *WatchRule) error {
	pm.logger.Info("running", "rule", rule.Name, "cmd", rule.Cmd)

	cmd := exec.CommandContext(ctx, "sh", "-c", rule.Cmd)
	cmd.Env = buildEnv(rule.Env)
	cmd.Stdout = &logWriter{logger: pm.logger, level: slog.LevelInfo, prefix: rule.Name}
	cmd.Stderr = &logWriter{logger: pm.logger, level: slog.LevelError, prefix: rule.Name}

	if err := cmd.Run(); err != nil {
		return fmt.Errorf("rule %q cmd failed: %w", rule.Name, err)
	}
	return nil
}

// StartProcess starts a long-running process, killing any previous instance.
// The process is tracked and can be stopped via StopAll.
func (pm *ProcessManager) StartProcess(ctx context.Context, rule *WatchRule) error {
	pm.stopProcess(rule.Name)

	pm.logger.Info("starting", "rule", rule.Name, "run", rule.Run)

	cmd := exec.CommandContext(ctx, "sh", "-c", rule.Run)
	cmd.Env = buildEnv(rule.Env)
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
	cmd.Stdout = &logWriter{logger: pm.logger, level: slog.LevelInfo, prefix: rule.Name}
	cmd.Stderr = &logWriter{logger: pm.logger, level: slog.LevelError, prefix: rule.Name}

	if err := cmd.Start(); err != nil {
		return fmt.Errorf("rule %q start failed: %w", rule.Name, err)
	}

	pm.mu.Lock()
	pm.procs[rule.Name] = cmd.Process
	pm.mu.Unlock()

	// Wait in background so we can clean up the map entry.
	go func() {
		_ = cmd.Wait()
		pm.mu.Lock()
		if pm.procs[rule.Name] == cmd.Process {
			delete(pm.procs, rule.Name)
		}
		pm.mu.Unlock()
	}()

	return nil
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
		proc.Wait()
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

// logWriter implements io.Writer and writes each line to slog.
type logWriter struct {
	logger *slog.Logger
	level  slog.Level
	prefix string
	buf    []byte
}

func (w *logWriter) Write(p []byte) (int, error) {
	w.buf = append(w.buf, p...)
	for {
		idx := strings.IndexByte(string(w.buf), '\n')
		if idx < 0 {
			break
		}
		line := string(w.buf[:idx])
		w.buf = w.buf[idx+1:]
		if line != "" {
			w.logger.Log(context.Background(), w.level, line, "rule", w.prefix)
		}
	}
	return len(p), nil
}

var _ io.Writer = (*logWriter)(nil)
