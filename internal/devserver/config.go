package devserver

import (
	"bufio"
	"errors"
	"fmt"
	"io/fs"
	"net"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/BurntSushi/toml"
)

// Config is the top-level hamr.toml configuration.
type Config struct {
	Dev   DevConfig   `toml:"dev"`
	Proxy ProxyConfig `toml:"proxy"`

	// ProxyConfigured is true when [proxy] was explicitly present in the TOML.
	// When false, the proxy is not started and no defaults are applied.
	ProxyConfigured bool `toml:"-"`
}

// DevConfig holds the [dev] table with watch rules and daemons.
type DevConfig struct {
	Watch           []WatchRule     `toml:"watch"`
	Daemons         []Daemon        `toml:"daemon"`
	DockerCompose   []DockerCompose `toml:"docker_compose"`
	LogFile         string          `toml:"log_file"`
	LogFileMaxLines int             `toml:"log_file_max_lines"`
	ProxyListen     string          `toml:"proxy_listen"`
	ProxyTarget     string          `toml:"proxy_target"`
	InjectReload    *bool           `toml:"inject_reload"`
	Email           EmailConfig     `toml:"email"`
	SMS             SMSConfig       `toml:"sms"`
	Stripe          StripeConfig    `toml:"stripe"`
	MCP             MCPConfig       `toml:"mcp"`

	// DarkFilter sets the initial state of the dev-panel "Dark filter"
	// toggle: an invert(1) hue-rotate(180deg) CSS filter over the proxied
	// site so a light-mode app is bearable to work on. Default false. The
	// panel toggle flips it at runtime in the hamr dev process only — the
	// value is never written back to hamr.toml.
	DarkFilter bool `toml:"dark_filter"`

	// HamrConsoleCapture toggles the entire browser-console transport
	// (window.console.* + uncaught errors + unhandled rejections +
	// resource-load failures + CSP violations → /__hamr/console WS →
	// `[site:console]` lines in the dev TUI / log file). Default true
	// (nil pointer): on by default in fresh scaffolds. Set false to skip
	// mounting the WS endpoint and to tell the injected reload script
	// not to patch console or open a connection — zero overhead.
	HamrConsoleCapture *bool `toml:"hamr_console_capture"`

	// HamrConsoleFilter, when true, drops browser console frames whose
	// message contains "[hamr]" — i.e. logs emitted by hamr's own injected
	// reload script. Default false: show everything the browser sees,
	// including hamr's own chatter. Flip true if the per-save chatter
	// (`[hamr] live reload connected`, `[hamr] page swapped`, etc.) is
	// noisy enough to drown out app-side logs. No effect when
	// HamrConsoleCapture is false.
	HamrConsoleFilter bool `toml:"hamr_console_filter"`

	// PortWalk toggles the +1-on-busy walk for hamr-managed ports
	// (proxy.listen, proxy.target / spawned-app PORT, and docker-compose
	// host-port publishes). Default true: when a port is busy hamr walks +1
	// up to a small cap and logs a WARN per shift, so two `hamr dev`
	// instances on the same machine don't collide. Set false to disable
	// walking and fail fast on EADDRINUSE — useful when CI or external
	// tooling pins a specific port and would mis-target if hamr silently
	// shifted.
	PortWalk *bool `toml:"port_walk"`
}

// PortWalkEnabled returns whether the +1-on-busy port walk is enabled.
// Defaults to true when the field is unset (nil) — opt-out, not opt-in.
func (c DevConfig) PortWalkEnabled() bool {
	if c.PortWalk == nil {
		return true
	}
	return *c.PortWalk
}

// HamrConsoleCaptureEnabled returns whether the browser-console transport
// is on. Defaults to true when the field is unset (nil) — opt-out, not
// opt-in.
func (c DevConfig) HamrConsoleCaptureEnabled() bool {
	if c.HamrConsoleCapture == nil {
		return true
	}
	return *c.HamrConsoleCapture
}

// EmailConfig holds the [dev.email] table for the mail mock. When Enabled
// is true, hamr dev runs an email inbox at /__hamr/mail on the reverse proxy.
// Requires [proxy] to be configured.
//
// Persistence defaults to on: the inbox is mirrored to an mbox file at
// PersistPath so it survives hamr dev restart. Set Persist=false for an
// ephemeral in-memory-only inbox.
type EmailConfig struct {
	Enabled         bool   `toml:"enabled"`
	MaxMessages     int    `toml:"max_messages"`      // default 500
	MaxMessageBytes int64  `toml:"max_message_bytes"` // default 10MiB
	Persist         *bool  `toml:"persist"`           // default true
	PersistPath     string `toml:"persist_path"`      // default ".hamr/mail/inbox.mbox"
}

// PersistEnabled returns whether persistence is on. Defaults to true when the
// field is unset (nil).
func (c EmailConfig) PersistEnabled() bool {
	if c.Persist == nil {
		return true
	}
	return *c.Persist
}

// ResolvedPersistPath returns PersistPath with the default applied.
func (c EmailConfig) ResolvedPersistPath() string {
	if c.PersistPath != "" {
		return c.PersistPath
	}
	return ".hamr/mail/inbox.mbox"
}

// SMSConfig holds the [dev.sms] table for the SMS mock. When Enabled is true,
// hamr dev runs an SMS inbox at /__hamr/sms on the reverse proxy. Requires
// [proxy] to be configured.
//
// Persistence defaults to on: the inbox is mirrored to a JSONL file at
// PersistPath so it survives hamr dev restart. Set Persist=false for an
// ephemeral in-memory-only inbox.
type SMSConfig struct {
	Enabled     bool   `toml:"enabled"`
	MaxMessages int    `toml:"max_messages"` // default 500
	Persist     *bool  `toml:"persist"`      // default true
	PersistPath string `toml:"persist_path"` // default ".hamr/sms/inbox.jsonl"
}

// PersistEnabled returns whether persistence is on. Defaults to true when the
// field is unset (nil) — matches the email mock's behaviour.
func (c SMSConfig) PersistEnabled() bool {
	if c.Persist == nil {
		return true
	}
	return *c.Persist
}

// ResolvedPersistPath returns PersistPath with the default applied.
func (c SMSConfig) ResolvedPersistPath() string {
	if c.PersistPath != "" {
		return c.PersistPath
	}
	return ".hamr/sms/inbox.jsonl"
}

// StripeConfig holds the [dev.stripe] table for the local Stripe mock. When
// Enabled is true, hamr dev mounts a Stripe-compatible HTTP backend on the
// proxy mux at /v1/* so real stripe-go clients can talk to it via
// stripe.SetBackend(...) pointing at the proxy URL. Requires [proxy] to be
// configured (the API and dev UI both live on the proxy mux).
//
// The mock is dev-only: no production safeguards. Apps gate by leaving
// STRIPE_MOCK unset in production so stripe-go reaches api.stripe.com.
//
// Webhook delivery: when an outcome is recorded (paid/failed/cancelled), the
// mock fires a real signed webhook to WebhookURL with WebhookSecret, exactly
// as Stripe would. The app's existing webhook handler (using stripe-go's
// webhook.ConstructEvent) verifies and processes it unchanged.
type StripeConfig struct {
	Enabled       bool   `toml:"enabled"`
	WebhookURL    string `toml:"webhook_url"`    // required when Enabled
	WebhookSecret string `toml:"webhook_secret"` // required when Enabled
	Persist       *bool  `toml:"persist"`        // default true
	PersistPath   string `toml:"persist_path"`   // default ".hamr/stripe/state.json"
}

// PersistEnabled returns whether persistence is on. Defaults to true when
// the field is unset (nil) — matches the email mock's behaviour.
func (c StripeConfig) PersistEnabled() bool {
	if c.Persist == nil {
		return true
	}
	return *c.Persist
}

// ResolvedPersistPath returns PersistPath with the default applied.
func (c StripeConfig) ResolvedPersistPath() string {
	if c.PersistPath != "" {
		return c.PersistPath
	}
	return ".hamr/stripe/state.json"
}

// DockerCompose declares a docker compose file that hamr ensures is running.
type DockerCompose struct {
	Name        string   `toml:"name"`
	File        string   `toml:"file"`
	Services    []string `toml:"services"`
	KeepRunning bool     `toml:"keep_running"`
	WaitReady   bool     `toml:"wait_ready"`
	Env         []string `toml:"env"`
}

// Daemon defines a long-running background process started once at launch.
type Daemon struct {
	Name string   `toml:"name"`
	Cmd  string   `toml:"cmd"`
	Dir  string   `toml:"dir"`
	Env  []string `toml:"env"`
}

// ProxyConfig holds the [proxy] table.
type ProxyConfig struct {
	Listen       string `toml:"listen"`
	Target       string `toml:"target"`
	InjectReload *bool  `toml:"inject_reload"`
}

// WatchRule defines a single watch/build/run rule.
//
// Dir sets the working directory for cmd and run, relative to the directory
// hamr dev runs in. It does NOT affect watch/ignore globs —
// those stay root-relative regardless, so a rule can watch the whole repo
// while building inside a subdirectory.
type WatchRule struct {
	Name     string        `toml:"name"`
	Watch    StringOrSlice `toml:"watch"`
	Ignore   StringOrSlice `toml:"ignore"`
	Cmd      string        `toml:"cmd"`
	Run      string        `toml:"run"`
	Dir      string        `toml:"dir"`
	Depends  StringOrSlice `toml:"depends"`
	Debounce Duration      `toml:"debounce"`
	Reload   ReloadScope   `toml:"reload"`
	Env      []string      `toml:"env"`
}

// StringOrSlice accepts either a single string or a list of strings in TOML.
type StringOrSlice []string

// UnmarshalTOML implements the toml.Unmarshaler interface.
func (s *StringOrSlice) UnmarshalTOML(data any) error {
	switch v := data.(type) {
	case string:
		*s = StringOrSlice{v}
	case []any:
		result := make([]string, 0, len(v))
		for _, item := range v {
			str, ok := item.(string)
			if !ok {
				return fmt.Errorf("expected string in array, got %T", item)
			}
			result = append(result, str)
		}
		*s = result
	default:
		return fmt.Errorf("expected string or array of strings, got %T", data)
	}
	return nil
}

// Duration wraps time.Duration with TOML unmarshaling that accepts
// an integer (milliseconds) or a Go duration string like "200ms".
type Duration struct {
	time.Duration
}

// UnmarshalTOML implements the toml.Unmarshaler interface.
func (d *Duration) UnmarshalTOML(data any) error {
	switch v := data.(type) {
	case int64:
		d.Duration = time.Duration(v) * time.Millisecond
	case string:
		parsed, err := time.ParseDuration(v)
		if err != nil {
			return fmt.Errorf("invalid duration %q: %w", v, err)
		}
		d.Duration = parsed
	default:
		return fmt.Errorf("expected int (ms) or duration string, got %T", data)
	}
	return nil
}

// ReloadScope controls what kind of browser reload a rule triggers.
// Values: "full", "css", "none", or a boolean (true="full", false="none").
type ReloadScope string

const (
	ReloadFull ReloadScope = "full"
	ReloadCSS  ReloadScope = "css"
	ReloadNone ReloadScope = "none"
)

// UnmarshalTOML implements the toml.Unmarshaler interface.
func (r *ReloadScope) UnmarshalTOML(data any) error {
	switch v := data.(type) {
	case bool:
		if v {
			*r = ReloadFull
		} else {
			*r = ReloadNone
		}
	case string:
		switch strings.ToLower(v) {
		case "full", "true":
			*r = ReloadFull
		case "css":
			*r = ReloadCSS
		case "none", "false":
			*r = ReloadNone
		default:
			return fmt.Errorf("invalid reload scope %q: must be \"full\", \"css\", or \"none\"", v)
		}
	default:
		return fmt.Errorf("expected bool or string for reload, got %T", data)
	}
	return nil
}

// PrefsFileName is the per-developer override read from the same directory
// as the main config. Gitignored by the scaffold: it holds preferences a
// developer wants locally without imposing them on the team.
const PrefsFileName = ".pref.hamr.toml"

// PrefsPathFor returns the override path that pairs with the given config
// file — a sibling of it, so `hamr dev --config /elsewhere/hamr.toml` reads
// /elsewhere/.pref.hamr.toml.
func PrefsPathFor(configPath string) string {
	return filepath.Join(filepath.Dir(configPath), PrefsFileName)
}

// LoadConfig reads and parses a hamr.toml file, merges the per-developer
// .pref.hamr.toml override over it if present, applies defaults, and
// validates.
func LoadConfig(path string) (*Config, error) { return loadConfig(path, true) }

// LoadConfigNoPrefs is LoadConfig without the .pref.hamr.toml merge. Use it
// anywhere the loaded values are written back to hamr.toml — merging first
// would promote a developer's gitignored local preference into the team's
// committed config.
func LoadConfigNoPrefs(path string) (*Config, error) { return loadConfig(path, false) }

func loadConfig(path string, withPrefs bool) (*Config, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("read config: %w", err)
	}

	var cfg Config
	meta, err := toml.Decode(string(data), &cfg)
	if err != nil {
		return nil, fmt.Errorf("parse config: %w", err)
	}

	// Decoding the override into the same struct gives a per-key merge at
	// any depth: only keys present in the override are touched.
	// ponytail: arrays of tables ([[dev.watch]], [[dev.docker_compose]])
	// replace the whole list rather than merging — decoder behaviour, not a
	// choice. Merge-by-name if partial rule overrides are ever needed.
	var prefsMeta toml.MetaData
	if withPrefs {
		if prefsMeta, err = mergePrefs(PrefsPathFor(path), &cfg); err != nil {
			return nil, err
		}
	}

	cfg.ProxyConfigured = proxyConfigured(meta) || proxyConfigured(prefsMeta)
	if err := applyProxyAliases(&cfg, path); err != nil {
		return nil, fmt.Errorf("validate config: %w", err)
	}
	applyDefaults(&cfg)
	if err := resolveProxyEnvRefs(&cfg, path); err != nil {
		return nil, fmt.Errorf("validate config: %w", err)
	}

	if err := validate(&cfg); err != nil {
		return nil, fmt.Errorf("validate config: %w", err)
	}
	// Validated against the process working directory, not the config file's
	// directory: `dir` ends up as cmd.Dir on a relative path, and watch globs
	// are cwd-relative too, so cwd is what the rule actually resolves against
	// under `hamr dev --config /elsewhere/hamr.toml`.
	if err := validateRuleDirs(&cfg, "."); err != nil {
		return nil, fmt.Errorf("validate config: %w", err)
	}

	return &cfg, nil
}

// mergePrefs decodes the .pref.hamr.toml override (if any) over cfg and
// returns its metadata. A missing file is not an error — the override is
// optional by design. The zero MetaData is safe to query.
func mergePrefs(path string, cfg *Config) (toml.MetaData, error) {
	data, err := os.ReadFile(path)
	if errors.Is(err, fs.ErrNotExist) {
		return toml.MetaData{}, nil
	}
	if err != nil {
		return toml.MetaData{}, fmt.Errorf("read %s: %w", PrefsFileName, err)
	}
	meta, err := toml.Decode(string(data), cfg)
	if err != nil {
		return toml.MetaData{}, fmt.Errorf("parse %s: %w", PrefsFileName, err)
	}
	return meta, nil
}

// proxyConfigured reports whether a decoded file mentions any proxy key, so
// an override that only sets a proxy field still flips the flag.
func proxyConfigured(meta toml.MetaData) bool {
	return meta.IsDefined("proxy") ||
		meta.IsDefined("dev", "proxy_listen") ||
		meta.IsDefined("dev", "proxy_target") ||
		meta.IsDefined("dev", "inject_reload")
}

// validateRuleDirs checks every rule/daemon `dir` against root (the working
// directory rules execute from). Split out of validate() because it is the
// only check that touches the filesystem.
func validateRuleDirs(cfg *Config, root string) error {
	for _, rule := range cfg.Dev.Watch {
		if err := validateRuleDir(root, rule.Dir); err != nil {
			return fmt.Errorf("watch rule %q: %w", rule.Name, err)
		}
	}
	for _, d := range cfg.Dev.Daemons {
		if err := validateRuleDir(root, d.Dir); err != nil {
			return fmt.Errorf("daemon %q: %w", d.Name, err)
		}
	}
	return nil
}

// validateRuleDir rejects a `dir` that is absolute, escapes root, or does not
// exist as a directory. Empty means "run in root" and always passes. The existence check is deliberate: silently falling back to the root
// on a typo (dir = "frontned") runs the build in the wrong place and the only
// symptom is a confusing command failure.
//
// Symlinks are resolved before the containment check so a symlinked dir can't
// be used to step outside the project.
func validateRuleDir(root, dir string) error {
	if dir == "" {
		return nil
	}
	if filepath.IsAbs(dir) {
		return fmt.Errorf("dir %q must be relative to the project root", dir)
	}
	absRoot, err := filepath.Abs(root)
	if err != nil {
		return fmt.Errorf("resolve project root: %w", err)
	}
	if resolved, err := filepath.EvalSymlinks(absRoot); err == nil {
		absRoot = resolved
	}
	full := filepath.Join(absRoot, dir)
	info, err := os.Stat(full)
	if err != nil {
		if os.IsNotExist(err) {
			return fmt.Errorf("dir %q does not exist", dir)
		}
		return fmt.Errorf("dir %q: %w", dir, err)
	}
	if !info.IsDir() {
		return fmt.Errorf("dir %q is not a directory", dir)
	}
	resolved, err := filepath.EvalSymlinks(full)
	if err != nil {
		return fmt.Errorf("dir %q: %w", dir, err)
	}
	rel, err := filepath.Rel(absRoot, resolved)
	if err != nil || rel == ".." || strings.HasPrefix(rel, ".."+string(filepath.Separator)) {
		return fmt.Errorf("dir %q resolves outside the project root", dir)
	}
	return nil
}

func applyProxyAliases(cfg *Config, configPath string) error {
	if cfg == nil {
		return nil
	}
	if cfg.Dev.ProxyListen != "" {
		if cfg.Proxy.Listen != "" && cfg.Proxy.Listen != cfg.Dev.ProxyListen {
			return fmt.Errorf("proxy.listen and dev.proxy_listen both set with different values")
		}
		cfg.Proxy.Listen = cfg.Dev.ProxyListen
	}
	if cfg.Dev.ProxyTarget != "" {
		if cfg.Proxy.Target != "" && cfg.Proxy.Target != cfg.Dev.ProxyTarget {
			return fmt.Errorf("proxy.target and dev.proxy_target both set with different values")
		}
		cfg.Proxy.Target = cfg.Dev.ProxyTarget
	}
	if cfg.Dev.InjectReload != nil {
		if cfg.Proxy.InjectReload != nil && *cfg.Proxy.InjectReload != *cfg.Dev.InjectReload {
			return fmt.Errorf("proxy.inject_reload and dev.inject_reload both set with different values")
		}
		cfg.Proxy.InjectReload = cfg.Dev.InjectReload
	}
	return nil
}

func resolveProxyEnvRefs(cfg *Config, configPath string) error {
	if cfg == nil || !cfg.ProxyConfigured {
		return nil
	}
	if ref, ok := parseEnvRef(cfg.Proxy.Listen); ok {
		v, err := resolveProxyAddrEnvRef(configPath, ref)
		if err != nil {
			return fmt.Errorf("proxy.listen env ref %q: %w", "$"+ref, err)
		}
		cfg.Proxy.Listen = v
	}
	if ref, ok := parseEnvRef(cfg.Proxy.Target); ok {
		v, err := resolveProxyAddrEnvRef(configPath, ref)
		if err != nil {
			return fmt.Errorf("proxy.target env ref %q: %w", "$"+ref, err)
		}
		cfg.Proxy.Target = v
	}
	return nil
}

func resolveProxyAddrEnvRef(configPath, key string) (string, error) {
	if v := os.Getenv(key); v != "" {
		return normalizeProxyAddrEnvValue(v)
	}
	dotenvPath := filepath.Join(filepath.Dir(configPath), ".env")
	if v, ok := readDotenvKey(dotenvPath, key); ok && v != "" {
		return normalizeProxyAddrEnvValue(v)
	}
	return "", fmt.Errorf("not found in shell env or %s", dotenvPath)
}

func parseEnvRef(v string) (string, bool) {
	v = strings.TrimSpace(v)
	if len(v) < 2 || v[0] != '$' {
		return "", false
	}
	return strings.TrimPrefix(v, "$"), true
}

func normalizeProxyAddrEnvValue(v string) (string, error) {
	v = strings.TrimSpace(v)
	if v == "" {
		return "", fmt.Errorf("empty env value")
	}
	if _, err := strconv.Atoi(v); err == nil {
		return ":" + v, nil
	}
	return v, nil
}

func readDotenvKey(path, key string) (string, bool) {
	f, err := os.Open(path)
	if err != nil {
		return "", false
	}
	defer f.Close() //nolint:errcheck

	scanner := bufio.NewScanner(f)
	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())
		if line == "" || line[0] == '#' {
			continue
		}
		k, v, ok := strings.Cut(line, "=")
		if !ok {
			continue
		}
		if strings.TrimSpace(k) != key {
			continue
		}
		v = strings.TrimSpace(v)
		if len(v) >= 2 && (v[0] == '"' || v[0] == '\'') && v[len(v)-1] == v[0] {
			v = v[1 : len(v)-1]
		}
		return v, true
	}
	return "", false
}

func applyDefaults(cfg *Config) {
	if cfg.ProxyConfigured {
		if cfg.Proxy.Listen == "" {
			// Loopback by default: the dev app + the /__hamr/* control surface
			// (incl. the MCP gateway) are reachable only from this machine.
			// Set proxy_listen = ":3000" to expose on the LAN (device testing).
			cfg.Proxy.Listen = "localhost:3000"
		}
		if cfg.Proxy.Target == "" {
			cfg.Proxy.Target = ":8080"
		}
		if cfg.Proxy.InjectReload == nil {
			t := true
			cfg.Proxy.InjectReload = &t
		}
	}

	if cfg.Dev.LogFile == "" {
		cfg.Dev.LogFile = ".hamr/dev_logs.txt"
	}
	if cfg.Dev.LogFileMaxLines == 0 {
		cfg.Dev.LogFileMaxLines = 200
	}

	for i := range cfg.Dev.Watch {
		if cfg.Dev.Watch[i].Debounce.Duration == 0 {
			cfg.Dev.Watch[i].Debounce.Duration = 100 * time.Millisecond
		}
	}
}

func validate(cfg *Config) error {
	if len(cfg.Dev.Watch) == 0 && len(cfg.Dev.Daemons) == 0 && len(cfg.Dev.DockerCompose) == 0 {
		return fmt.Errorf("no watch rules, daemons, or docker compose entries defined in [dev]")
	}
	if cfg.Dev.LogFileMaxLines < 0 {
		return fmt.Errorf("[dev] log_file_max_lines must be greater than 0")
	}
	if err := cfg.Dev.MCP.validate(); err != nil {
		return err
	}

	names := make(map[string]bool, len(cfg.Dev.Watch)+len(cfg.Dev.Daemons)+len(cfg.Dev.DockerCompose))

	// Validate docker compose entries.
	for i, dc := range cfg.Dev.DockerCompose {
		if dc.Name == "" {
			return fmt.Errorf("docker_compose %d: name is required", i)
		}
		if dc.File == "" {
			return fmt.Errorf("docker_compose %q: file is required", dc.Name)
		}
		if names[dc.Name] {
			return fmt.Errorf("duplicate name %q (docker_compose collides with another entry)", dc.Name)
		}
		names[dc.Name] = true
	}

	// Validate daemons.
	for i, d := range cfg.Dev.Daemons {
		if d.Name == "" {
			return fmt.Errorf("daemon %d: name is required", i)
		}
		if d.Cmd == "" {
			return fmt.Errorf("daemon %q: cmd is required", d.Name)
		}
		if names[d.Name] {
			return fmt.Errorf("duplicate daemon name %q", d.Name)
		}
		names[d.Name] = true
	}

	// Validate watch rules.
	for i, rule := range cfg.Dev.Watch {
		if rule.Name == "" {
			return fmt.Errorf("watch rule %d: name is required", i)
		}
		if names[rule.Name] {
			return fmt.Errorf("duplicate name %q (watch rule collides with daemon or other watch rule)", rule.Name)
		}
		names[rule.Name] = true

		if len(rule.Watch) == 0 {
			return fmt.Errorf("watch rule %q: at least one watch pattern is required", rule.Name)
		}
		if rule.Cmd == "" && rule.Run == "" {
			return fmt.Errorf("watch rule %q: cmd or run is required", rule.Name)
		}
	}

	// Check unknown deps (only watch rule names are valid deps).
	watchNames := make(map[string]bool, len(cfg.Dev.Watch))
	for _, rule := range cfg.Dev.Watch {
		watchNames[rule.Name] = true
	}
	for _, rule := range cfg.Dev.Watch {
		for _, dep := range rule.Depends {
			if !watchNames[dep] {
				return fmt.Errorf("watch rule %q: unknown dependency %q", rule.Name, dep)
			}
		}
	}

	// Cycle detection via Kahn's algorithm.
	if err := detectCycles(cfg.Dev.Watch); err != nil {
		return err
	}

	// Validate proxy (only when explicitly configured).
	if cfg.ProxyConfigured {
		if cfg.Proxy.Listen != "" {
			if err := validateAddr(cfg.Proxy.Listen); err != nil {
				return fmt.Errorf("proxy.listen: %w", err)
			}
		}
		if cfg.Proxy.Target != "" {
			if err := validateAddr(cfg.Proxy.Target); err != nil {
				return fmt.Errorf("proxy.target: %w", err)
			}
		}
	}

	// Validate Stripe mock. Required fields fire only when enabled — leaving
	// the block out (or enabled=false) is the no-op path.
	if cfg.Dev.Stripe.Enabled {
		if cfg.Dev.Stripe.WebhookURL == "" {
			return fmt.Errorf("dev.stripe.webhook_url is required when dev.stripe.enabled = true (point at your app's stripe webhook handler, e.g. \"http://localhost:8080/api/webhooks/stripe\")")
		}
		if cfg.Dev.Stripe.WebhookSecret == "" {
			return fmt.Errorf("dev.stripe.webhook_secret is required when dev.stripe.enabled = true (must match your app's STRIPE_WEBHOOK_SECRET)")
		}
	}

	// Email / SMS / Stripe mocks mount their routes on the proxy mux and derive
	// HAMR_DEV_URL / HAMR_STRIPE_MOCK_URL from the actual bound port. A
	// proxy section is still required (the mock UIs live on the proxy
	// mux), but ":0" / random-bind is now allowed — the runner derives
	// the URL after the listener has bound rather than at config-load.
	if cfg.Dev.Email.Enabled || cfg.Dev.SMS.Enabled || cfg.Dev.Stripe.Enabled {
		if !cfg.ProxyConfigured {
			return fmt.Errorf("[proxy] is required when [dev.email], [dev.sms], or [dev.stripe] is enabled: the mocks live on the proxy mux and their client-reachable URL is derived from the bound proxy port")
		}
	}

	return nil
}
func detectCycles(rules []WatchRule) error {
	inDegree := make(map[string]int)
	dependees := make(map[string][]string)

	for _, r := range rules {
		if _, ok := inDegree[r.Name]; !ok {
			inDegree[r.Name] = 0
		}
		for _, dep := range r.Depends {
			inDegree[r.Name]++
			dependees[dep] = append(dependees[dep], r.Name)
		}
	}

	var queue []string
	for name, deg := range inDegree {
		if deg == 0 {
			queue = append(queue, name)
		}
	}

	visited := 0
	for len(queue) > 0 {
		node := queue[0]
		queue = queue[1:]
		visited++

		for _, dep := range dependees[node] {
			inDegree[dep]--
			if inDegree[dep] == 0 {
				queue = append(queue, dep)
			}
		}
	}

	if visited != len(rules) {
		return fmt.Errorf("dependency cycle detected among watch rules")
	}
	return nil
}

func validateAddr(addr string) error {
	// net.SplitHostPort handles IPv6 literals ("[::1]:3000") and the bare
	// ":port" form, unlike splitting on the first colon (which rejected every
	// IPv6 address even though net.Listen accepts them).
	_, port, err := net.SplitHostPort(addr)
	if err != nil {
		return fmt.Errorf("invalid address %q: must be host:port or :port: %w", addr, err)
	}
	n, err := strconv.Atoi(port)
	if err != nil {
		return fmt.Errorf("invalid port %q: %w", port, err)
	}
	if n < 0 || n > 65535 {
		return fmt.Errorf("port %d out of range", n)
	}
	return nil
}
