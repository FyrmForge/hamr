package devserver

import (
	"bufio"
	"bytes"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
)

// dotenvEntry is one parsed line from a .env file. Order is preserved by
// the parser; Value is post-quote-strip.
type dotenvEntry struct {
	Key   string
	Value string
}

// hostPortRE matches "(localhost|127.0.0.1|0.0.0.0|[::1]):<port>" with a
// trailing word boundary so :5432 doesn't match :54321. Port is captured for
// per-port lookup in the rewrite callback.
var hostPortRE = regexp.MustCompile(`(localhost|127\.0\.0\.1|0\.0\.0\.0|\[::1\]):(\d+)\b`)

// wholeValuePortRE matches values that are exactly ":<port>" (Go listener
// convention for "all interfaces, this port"). Anchored so substrings inside
// longer values don't false-positive.
var wholeValuePortRE = regexp.MustCompile(`^:(\d+)$`)

// portShifts is old-host-port → new-host-port. Walks across the proxy, app,
// and any docker-compose host port collapse into one map; the rewrite
// engine applies them in a single pass so cascading shifts (5432→5433,
// 5433→5434) don't compound on a single value.
type portShifts map[int]int

// loadDotenv reads path and returns parsed entries in declaration order.
// Missing file is not an error — returns (nil, nil) so callers can treat
// "no .env" as "no rewrites needed". Comment-only and malformed lines are
// silently skipped.
func loadDotenv(path string) ([]dotenvEntry, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, err
	}
	return parseDotenv(data), nil
}

func parseDotenv(data []byte) []dotenvEntry {
	var out []dotenvEntry
	scanner := bufio.NewScanner(bytes.NewReader(data))
	for scanner.Scan() {
		if key, val, ok := parseDotenvLine(scanner.Text()); ok {
			out = append(out, dotenvEntry{Key: key, Value: val})
		}
	}
	return out
}

// ReadDotenvKey returns the first value for key in the .env file at path.
// ok is false on a miss or an unreadable file. It never touches the process
// env, so callers read credentials only where they need them.
func ReadDotenvKey(path, key string) (string, bool) {
	entries, err := loadDotenv(path)
	if err != nil {
		return "", false
	}
	for _, e := range entries {
		if e.Key == key {
			return e.Value, true
		}
	}
	return "", false
}

// parseDotenvLine is the one .env line parser hamr uses, so hamr reads the
// same values the app does through godotenv: an optional `export ` prefix,
// surrounding quotes stripped, and a ` #` comment dropped after an unquoted
// value. Blank, comment and malformed lines return ok=false.
// ponytail: no escape sequences or multi-line quoted values; add them if a
// real .env needs them.
func parseDotenvLine(line string) (key, val string, ok bool) {
	line = strings.TrimSpace(line)
	if line == "" || line[0] == '#' {
		return "", "", false
	}
	k, v, found := strings.Cut(line, "=")
	if !found {
		return "", "", false
	}
	key = strings.TrimSpace(strings.TrimPrefix(strings.TrimSpace(k), "export "))
	if key == "" {
		return "", "", false
	}
	v = strings.TrimSpace(v)
	if len(v) >= 2 && (v[0] == '"' || v[0] == '\'') {
		// The first unescaped matching quote closes the value; the rest of the
		// line (a comment) is dropped, like godotenv.
		for i := 1; i < len(v); i++ {
			if v[i] == '\\' {
				i++
				continue
			}
			if v[i] == v[0] {
				return key, v[1:i], true
			}
		}
	}
	for i := 1; i < len(v); i++ {
		if v[i] == '#' && (v[i-1] == ' ' || v[i-1] == '\t') {
			v = strings.TrimSpace(v[:i])
			break
		}
	}
	return key, v, true
}

// rewriteForPortShifts walks entries, applying the agreed match rules for
// each shift in a single pass. Returns KEY=VALUE strings only for entries
// whose value actually changed — entries that didn't match any rule are
// omitted, so the caller can append the result to an injected-env list
// without overriding values that were already correct.
//
// Match rules:
//   - "(localhost|127.0.0.1|0.0.0.0|[::1]):<oldPort>" anywhere in the value,
//     port-boundary-anchored — port swapped, host preserved.
//   - whole-value "^:<oldPort>$" — port swapped (Go listener form).
//
// Both rules are applied; the same value can be rewritten by both (rare).
// Cascading shifts (5432→5433, 5433→5434) are handled correctly because
// the regex callback looks up each captured port in shifts once — no
// re-replacement of an already-substituted value.
func rewriteForPortShifts(entries []dotenvEntry, shifts portShifts) []string {
	if len(entries) == 0 || len(shifts) == 0 {
		return nil
	}
	var out []string
	for _, e := range entries {
		newValue := applyShiftsToValue(e.Value, shifts)
		if newValue == e.Value {
			continue
		}
		out = append(out, e.Key+"="+newValue)
	}
	return out
}

func applyShiftsToValue(value string, shifts portShifts) string {
	if v, ok := rewriteWholeValuePort(value, shifts); ok {
		return v
	}
	return hostPortRE.ReplaceAllStringFunc(value, func(match string) string {
		sub := hostPortRE.FindStringSubmatch(match)
		host, portStr := sub[1], sub[2]
		port, err := strconv.Atoi(portStr)
		if err != nil {
			return match
		}
		newPort, ok := shifts[port]
		if !ok {
			return match
		}
		return host + ":" + strconv.Itoa(newPort)
	})
}

func rewriteWholeValuePort(value string, shifts portShifts) (string, bool) {
	sub := wholeValuePortRE.FindStringSubmatch(value)
	if sub == nil {
		return value, false
	}
	port, err := strconv.Atoi(sub[1])
	if err != nil {
		return value, false
	}
	newPort, ok := shifts[port]
	if !ok {
		return value, false
	}
	return ":" + strconv.Itoa(newPort), true
}

// resolveDotenvInjection reads .env at path, applies shifts, and returns
// the KEY=VALUE rewrites ready to append to the injected-env list. Errors
// loading the file are returned to the caller; missing file is not an
// error (returns nil, nil).
func resolveDotenvInjection(path string, shifts portShifts) ([]string, error) {
	if len(shifts) == 0 {
		return nil, nil
	}
	entries, err := loadDotenv(path)
	if err != nil {
		return nil, fmt.Errorf("read %s: %w", path, err)
	}
	return rewriteForPortShifts(entries, shifts), nil
}

// ResolveEnvRewrites loads .hamr/walks.json and .env from dir, applies the
// active port-walk rewrite rules, and returns the rewritten KEY=VALUE pairs
// the spawned-children injection would emit. Empty result (nil, nil) when
// nothing walked or no .env present — consumers can use the result
// unconditionally; an empty slice makes their downstream a no-op.
//
// This is the canonical entry point for callers outside the package
// (cmd/env, etc.). Match rules and limitations are documented on the
// per-rule helpers.
func ResolveEnvRewrites(dir string) ([]string, error) {
	records, err := readWalks(dir)
	if err != nil {
		return nil, fmt.Errorf("read walks: %w", err)
	}
	if len(records) == 0 {
		return nil, nil
	}
	shifts := shiftsToMap(records)
	if len(shifts) == 0 {
		return nil, nil
	}
	entries, err := loadDotenv(filepath.Join(dir, ".env"))
	if err != nil {
		return nil, fmt.Errorf("read .env: %w", err)
	}
	return rewriteForPortShifts(entries, shifts), nil
}

// RewriteValueForWalks applies the active port-walk rewrites to a single
// value (typically one read out of .env by hamr sync). Returns the value
// unchanged when no walks file is present or when nothing in the value
// matches a walked port. Errors loading walks.json are swallowed: a
// malformed file shouldn't break a CLI invocation that has a perfectly
// good fallback in the literal value the caller already has.
func RewriteValueForWalks(dir, value string) string {
	records, err := readWalks(dir)
	if err != nil || len(records) == 0 {
		return value
	}
	shifts := shiftsToMap(records)
	if len(shifts) == 0 {
		return value
	}
	return applyShiftsToValue(value, shifts)
}
