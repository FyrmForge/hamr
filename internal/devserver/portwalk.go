package devserver

import (
	"errors"
	"fmt"
	"log/slog"
	"net"
	"strconv"
	"strings"
	"syscall"
)

// maxPortWalkAttempts caps the +1 walk so a misconfigured machine that has
// every port in a wide range bound doesn't stall hamr dev forever. 50 covers
// any realistic dev box (proxy 3000 → 3049 etc.) without burning attempts.
const maxPortWalkAttempts = 50

// listenWalk binds a TCP listener at addr, walking +1 on the port up to
// maxAttempts times on EADDRINUSE. Returns the listener and the actual port
// it bound to. When maxAttempts is 1, this behaves like a plain net.Listen —
// useful for honoring `[dev].port_walk = false`. Logs WARN on every shift via
// log so the user sees the walk happen.
//
// addr accepts ":port", "host:port", or anything else net.Listen accepts.
// When the resolved port is 0 (":0", "127.0.0.1:0"), the OS picks a free
// port on the first attempt — no walking needed.
//
// Errors other than EADDRINUSE bubble up immediately (e.g. permission denied
// on a privileged port). When the cap is hit, returns an error naming the
// last attempt so the user can tell where the walk gave up.
func listenWalk(addr string, maxAttempts int, log *slog.Logger) (net.Listener, int, error) {
	if maxAttempts < 1 {
		maxAttempts = 1
	}
	host, startPort, err := splitListenAddr(addr)
	if err != nil {
		return nil, 0, fmt.Errorf("parse listen addr %q: %w", addr, err)
	}
	if startPort == 0 {
		ln, err := net.Listen("tcp", addr)
		if err != nil {
			return nil, 0, err
		}
		return ln, listenerPort(ln), nil
	}

	for attempt := 0; attempt < maxAttempts; attempt++ {
		port := startPort + attempt
		tryAddr := joinHostPort(host, port)
		ln, err := net.Listen("tcp", tryAddr)
		if err == nil {
			if attempt > 0 && log != nil {
				log.Info("port walked", "from", joinHostPort(host, startPort), "to", tryAddr)
			}
			return ln, port, nil
		}
		if !isAddrInUse(err) {
			return nil, 0, err
		}
		if log != nil && maxAttempts > 1 {
			log.Warn("port busy, walking", "addr", tryAddr, "next", joinHostPort(host, port+1))
		}
	}
	if maxAttempts == 1 {
		return nil, 0, fmt.Errorf("address %s already in use (set [dev].port_walk = true to walk +1 on busy)", joinHostPort(host, startPort))
	}
	return nil, 0, fmt.Errorf("could not bind any port in range %d-%d (all in use)",
		startPort, startPort+maxAttempts-1)
}

// probeFreePort returns the first free port at-or-after start on host,
// walking +1 up to maxAttempts on EADDRINUSE. Listens briefly to probe, then
// closes — caller spawns a process that will bind the returned port. There
// is a tiny race window between probe and bind; on collision the spawned
// process will fail and the user will see the error in dev logs (acceptable
// for the dev workflow).
//
// Used for ports hamr doesn't bind itself but needs to inject as env vars
// (PORT for the spawned app process, etc.). With maxAttempts == 1, behaves
// as a single-shot probe — returns ErrNotAvailable on busy.
// taken, when non-nil, marks ports (by hostPortKey) that the caller has already
// handed out earlier in the same batch. probeFreePort only checks OS-level bind
// availability — it binds then immediately closes — so without this a second
// caller probing the same base would see the port free and collide. Ports in
// taken are treated exactly like an in-use port: skipped, walked past, and
// counted toward maxAttempts so the walk window stays honest.
func probeFreePort(host string, start, maxAttempts int, taken map[string]bool, log *slog.Logger) (int, error) {
	if maxAttempts < 1 {
		maxAttempts = 1
	}
	if start == 0 {
		// :0 means "OS picks" — bind transiently to discover an actual port.
		ln, err := net.Listen("tcp", joinHostPort(host, 0))
		if err != nil {
			return 0, err
		}
		port := listenerPort(ln)
		_ = ln.Close()
		return port, nil
	}
	for attempt := 0; attempt < maxAttempts; attempt++ {
		port := start + attempt
		if taken[hostPortKey(host, port)] {
			if log != nil && maxAttempts > 1 {
				log.Warn("port already assigned this run, walking", "addr", joinHostPort(host, port), "next", joinHostPort(host, port+1))
			}
			continue
		}
		ln, err := net.Listen("tcp", joinHostPort(host, port))
		if err == nil {
			_ = ln.Close()
			if attempt > 0 && log != nil {
				log.Info("port walked", "from", joinHostPort(host, start), "to", joinHostPort(host, port))
			}
			return port, nil
		}
		if !isAddrInUse(err) {
			return 0, err
		}
		if log != nil && maxAttempts > 1 {
			log.Warn("port busy, walking", "addr", joinHostPort(host, port), "next", joinHostPort(host, port+1))
		}
	}
	if maxAttempts == 1 {
		return 0, fmt.Errorf("port %s already in use (set [dev].port_walk = true to walk +1 on busy)", joinHostPort(host, start))
	}
	return 0, fmt.Errorf("could not find a free port in range %d-%d (all in use)",
		start, start+maxAttempts-1)
}

// splitListenAddr parses a listen address into (host, port). Accepts ":port"
// (host empty), "host:port", "0.0.0.0:port", "[::]:port". An empty addr is
// rejected — caller should always pass something.
func splitListenAddr(addr string) (string, int, error) {
	if addr == "" {
		return "", 0, fmt.Errorf("empty addr")
	}
	host, portStr, err := net.SplitHostPort(addr)
	if err != nil {
		// Tolerate the bare ":port" form that net.Listen accepts.
		if strings.HasPrefix(addr, ":") {
			host = ""
			portStr = strings.TrimPrefix(addr, ":")
		} else {
			return "", 0, err
		}
	}
	port, err := strconv.Atoi(portStr)
	if err != nil {
		return "", 0, fmt.Errorf("invalid port %q: %w", portStr, err)
	}
	if port < 0 || port > 65535 {
		return "", 0, fmt.Errorf("port %d out of range", port)
	}
	return host, port, nil
}

// joinHostPort rebuilds a listen address from host (may be empty) and port.
// Mirrors the parsing splitListenAddr does — empty host renders as ":port".
func joinHostPort(host string, port int) string {
	if host == "" {
		return ":" + strconv.Itoa(port)
	}
	return net.JoinHostPort(host, strconv.Itoa(port))
}

// listenerPort returns the actual TCP port a listener is bound to.
// Panics if the listener isn't a *net.TCPListener — only call on listeners
// created via net.Listen("tcp", …).
func listenerPort(ln net.Listener) int {
	return ln.Addr().(*net.TCPAddr).Port
}

// isAddrInUse returns true when err is the OS "address already in use"
// error. Walk-up logic only walks on this — anything else (permission,
// network down) bubbles up immediately.
func isAddrInUse(err error) bool {
	return errors.Is(err, syscall.EADDRINUSE)
}

// portWalkAttempts returns the max-attempts value to pass to listenWalk /
// probeFreePort given a config. When [dev].port_walk is false, callers get
// a single attempt (strict-fail-on-busy); otherwise the full cap.
func portWalkAttempts(cfg *Config) int {
	if cfg != nil && !cfg.Dev.PortWalkEnabled() {
		return 1
	}
	return maxPortWalkAttempts
}
