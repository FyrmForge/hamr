package devserver

import (
	"fmt"
	"os"
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
	Watch         []WatchRule      `toml:"watch"`
	Daemons       []Daemon         `toml:"daemon"`
	DockerCompose []DockerCompose  `toml:"docker_compose"`
}

// DockerCompose declares a docker compose file that hamr ensures is running.
type DockerCompose struct {
	Name        string   `toml:"name"`
	File        string   `toml:"file"`
	Services    []string `toml:"services"`
	KeepRunning bool     `toml:"keep_running"`
	Env         []string `toml:"env"`
}

// Daemon defines a long-running background process started once at launch.
type Daemon struct {
	Name string   `toml:"name"`
	Cmd  string   `toml:"cmd"`
	Env  []string `toml:"env"`
}

// ProxyConfig holds the [proxy] table.
type ProxyConfig struct {
	Listen       string `toml:"listen"`
	Target       string `toml:"target"`
	InjectReload *bool  `toml:"inject_reload"`
}

// WatchRule defines a single watch/build/run rule.
type WatchRule struct {
	Name     string        `toml:"name"`
	Watch    StringOrSlice `toml:"watch"`
	Ignore   StringOrSlice `toml:"ignore"`
	Cmd      string        `toml:"cmd"`
	Run      string        `toml:"run"`
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

// LoadConfig reads and parses a hamr.toml file, applies defaults, and validates.
func LoadConfig(path string) (*Config, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("read config: %w", err)
	}

	var cfg Config
	meta, err := toml.Decode(string(data), &cfg)
	if err != nil {
		return nil, fmt.Errorf("parse config: %w", err)
	}

	cfg.ProxyConfigured = meta.IsDefined("proxy")
	applyDefaults(&cfg)

	if err := validate(&cfg); err != nil {
		return nil, fmt.Errorf("validate config: %w", err)
	}

	return &cfg, nil
}

func applyDefaults(cfg *Config) {
	if cfg.ProxyConfigured {
		if cfg.Proxy.Listen == "" {
			cfg.Proxy.Listen = ":3000"
		}
		if cfg.Proxy.Target == "" {
			cfg.Proxy.Target = ":8080"
		}
		if cfg.Proxy.InjectReload == nil {
			t := true
			cfg.Proxy.InjectReload = &t
		}
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
	if !strings.Contains(addr, ":") {
		return fmt.Errorf("invalid address %q: must be host:port or :port", addr)
	}
	parts := strings.SplitN(addr, ":", 2)
	port := parts[len(parts)-1]
	n, err := strconv.Atoi(port)
	if err != nil {
		return fmt.Errorf("invalid port %q: %w", port, err)
	}
	if n < 0 || n > 65535 {
		return fmt.Errorf("port %d out of range", n)
	}
	return nil
}
