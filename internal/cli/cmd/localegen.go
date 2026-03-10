package cmd

import (
	"encoding/json"
	"fmt"
	"go/format"
	"os"
	"path/filepath"
	"slices"
	"sort"
	"strings"
	"unicode"

	"github.com/BurntSushi/toml"
	"github.com/FyrmForge/hamr/pkg/i18n"
	"github.com/spf13/cobra"
)

// localeTomlConfig mirrors the [locale] section of hamr.toml.
type localeTomlConfig struct {
	Default string `toml:"default"`
	Dir     string `toml:"dir"`
	Output  string `toml:"output"`
	Package string `toml:"package"`
}

type localeTomlFile struct {
	Locale localeTomlConfig `toml:"locale"`
}

// localeKeyInfo describes a single translation key for code generation.
type localeKeyInfo struct {
	key      string
	isPlural bool
	params   []string // interpolation params (excluding Count)
}

var localeGenCmd = &cobra.Command{
	Use:   "gen",
	Short: "Generate type-safe Go accessors from locale JSON files",
	Args:  cobra.NoArgs,
	RunE:  runLocaleGen,
}

func init() {
	localeGenCmd.Flags().String("config", "hamr.toml", "path to hamr.toml config file")
	localeGenCmd.Flags().String("dir", "", "locale directory (overrides config)")
	localeGenCmd.Flags().String("out", "", "output file (overrides config)")
}

func runLocaleGen(cmd *cobra.Command, _ []string) error {
	configPath, _ := cmd.Flags().GetString("config")
	dirFlag, _ := cmd.Flags().GetString("dir")
	outFlag, _ := cmd.Flags().GetString("out")

	cfg := localeTomlConfig{
		Default: "en",
		Dir:     "locales",
		Output:  "internal/locale/locale.go",
		Package: "locale",
	}

	// Load config from TOML if present.
	data, err := os.ReadFile(configPath)
	if err != nil {
		if cmd.Flags().Changed("config") {
			return fmt.Errorf("reading config %s: %w", configPath, err)
		}
		// Default path — config is optional, use built-in defaults.
	} else {
		var f localeTomlFile
		if err := toml.Unmarshal(data, &f); err != nil {
			return fmt.Errorf("parsing %s: %w", configPath, err)
		}
		if f.Locale.Default != "" {
			cfg.Default = f.Locale.Default
		}
		if f.Locale.Dir != "" {
			cfg.Dir = f.Locale.Dir
		}
		if f.Locale.Output != "" {
			cfg.Output = f.Locale.Output
		}
		if f.Locale.Package != "" {
			cfg.Package = f.Locale.Package
		}
	}

	// Flag overrides.
	if dirFlag != "" {
		cfg.Dir = dirFlag
	}
	if outFlag != "" {
		cfg.Output = outFlag
	}

	// Load default locale JSON.
	defaultPath := filepath.Join(cfg.Dir, cfg.Default+".json")
	data, err = os.ReadFile(defaultPath)
	if err != nil {
		return fmt.Errorf("read default locale %s: %w", defaultPath, err)
	}
	var raw map[string]any
	if err := json.Unmarshal(data, &raw); err != nil {
		return fmt.Errorf("parse %s: %w", defaultPath, err)
	}

	// Flatten keys.
	keys, err := flattenForGen(raw, "")
	if err != nil {
		return fmt.Errorf("flatten %s: %w", defaultPath, err)
	}
	sort.Slice(keys, func(i, j int) bool { return keys[i].key < keys[j].key })

	// Validate other locales.
	entries, err := os.ReadDir(cfg.Dir)
	if err != nil {
		return fmt.Errorf("read locale dir: %w", err)
	}

	// Validate non-default locales (warn-only for missing, error for
	// interpolation mismatches).
	var hasError bool
	for _, e := range entries {
		if e.IsDir() || !strings.HasSuffix(e.Name(), ".json") {
			continue
		}
		name := strings.TrimSuffix(e.Name(), ".json")
		if name == cfg.Default {
			continue
		}
		path := filepath.Join(cfg.Dir, e.Name())
		localeRaw, err := os.ReadFile(path)
		if err != nil {
			fmt.Fprintf(os.Stderr, "warning: could not read %s: %v\n", path, err)
			continue
		}
		var localeData map[string]any
		if err := json.Unmarshal(localeRaw, &localeData); err != nil {
			fmt.Fprintf(os.Stderr, "warning: could not parse %s: %v\n", path, err)
			continue
		}
		localeFlat, err := flattenForGen(localeData, "")
		if err != nil {
			fmt.Fprintf(os.Stderr, "warning: could not flatten %s: %v\n", path, err)
			continue
		}

		localeKeyMap := make(map[string]localeKeyInfo, len(localeFlat))
		for _, k := range localeFlat {
			localeKeyMap[k.key] = k
		}

		for _, dk := range keys {
			lk, ok := localeKeyMap[dk.key]
			if !ok {
				fmt.Fprintf(os.Stderr, "warning: [%s] missing key %q (will fall back to %s)\n", name, dk.key, cfg.Default)
				continue
			}
			if !slices.Equal(dk.params, lk.params) {
				fmt.Fprintf(os.Stderr, "error: [%s] key %q: interpolation mismatch: %s has %v, %s has %v\n",
					name, dk.key, cfg.Default, dk.params, name, lk.params)
				hasError = true
			}
		}
	}

	if hasError {
		return fmt.Errorf("locale validation failed — fix interpolation mismatches above")
	}

	// Generate Go source.
	src := generateGoSource(cfg.Package, keys)
	formatted, err := format.Source([]byte(src))
	if err != nil {
		return fmt.Errorf("format generated source: %w (raw source written for debugging)", err)
	}

	if err := os.MkdirAll(filepath.Dir(cfg.Output), 0o755); err != nil {
		return fmt.Errorf("create output dir: %w", err)
	}
	if err := os.WriteFile(cfg.Output, formatted, 0o644); err != nil {
		return fmt.Errorf("write %s: %w", cfg.Output, err)
	}

	fmt.Printf("Generated %s with %d keys\n", cfg.Output, len(keys))
	return nil
}

// flattenForGen recursively flattens JSON into key info for code generation.
func flattenForGen(data map[string]any, prefix string) ([]localeKeyInfo, error) {
	var out []localeKeyInfo
	for k, v := range data {
		if strings.HasPrefix(k, "_") {
			continue
		}
		key := k
		if prefix != "" {
			key = prefix + "." + k
		}
		switch val := v.(type) {
		case string:
			out = append(out, localeKeyInfo{
				key:    key,
				params: extractInterpolationParams(val),
			})
		case map[string]any:
			if i18n.IsPluralObject(val) {
				var params []string
				for _, s := range val {
					params = mergeParams(params, extractInterpolationParams(s.(string)))
				}
				sort.Strings(params)
				out = append(out, localeKeyInfo{
					key:      key,
					isPlural: true,
					params:   params,
				})
			} else {
				nested, err := flattenForGen(val, key)
				if err != nil {
					return nil, err
				}
				out = append(out, nested...)
			}
		default:
			return nil, fmt.Errorf("key %s: unsupported type %T", key, v)
		}
	}
	return out, nil
}

// extractInterpolationParams returns sorted interpolation variable names,
// excluding "Count" which is auto-injected for plural messages.
func extractInterpolationParams(s string) []string {
	vars := i18n.InterpolationVars(s)
	return slices.DeleteFunc(vars, func(v string) bool { return v == "Count" })
}

func mergeParams(a, b []string) []string {
	seen := map[string]bool{}
	for _, s := range a {
		seen[s] = true
	}
	out := append([]string{}, a...)
	for _, s := range b {
		if !seen[s] {
			out = append(out, s)
		}
	}
	return out
}

func generateGoSource(pkg string, keys []localeKeyInfo) string {
	var b strings.Builder

	b.WriteString("// Code generated by hamr locale gen; DO NOT EDIT.\n\n")
	fmt.Fprintf(&b, "//go:generate hamr locale gen\n\n")
	fmt.Fprintf(&b, "package %s\n\n", pkg)
	b.WriteString("import \"github.com/FyrmForge/hamr/pkg/i18n\"\n\n")
	b.WriteString("// T wraps an i18n.Translator with type-safe accessor methods.\n")
	b.WriteString("type T struct {\n\ttr *i18n.Translator\n}\n\n")
	b.WriteString("// New wraps a Translator.\n")
	b.WriteString("func New(tr *i18n.Translator) *T { return &T{tr: tr} }\n\n")
	b.WriteString("// Translator returns the underlying Translator.\n")
	b.WriteString("func (t *T) Translator() *i18n.Translator { return t.tr }\n\n")

	for _, k := range keys {
		methodName := keyToMethodName(k.key)

		switch {
		case k.isPlural:
			params := "count int"
			callArgs := "count"
			if len(k.params) > 0 {
				for _, p := range k.params {
					params += ", " + toLowerFirst(p) + " string"
				}
				callArgs += ", map[string]any{"
				for i, p := range k.params {
					if i > 0 {
						callArgs += ", "
					}
					callArgs += fmt.Sprintf("%q: %s", p, toLowerFirst(p))
				}
				callArgs += "}"
			}
			fmt.Fprintf(&b, "// %s returns the translation for %q (plural).\n", methodName, k.key)
			fmt.Fprintf(&b, "func (t *T) %s(%s) string {\n", methodName, params)
			fmt.Fprintf(&b, "\treturn t.tr.T(%q, %s)\n", k.key, callArgs)
			b.WriteString("}\n\n")
		case len(k.params) > 0:
			var paramList []string
			var mapEntries []string
			for _, p := range k.params {
				paramList = append(paramList, toLowerFirst(p)+" string")
				mapEntries = append(mapEntries, fmt.Sprintf("%q: %s", p, toLowerFirst(p)))
			}
			fmt.Fprintf(&b, "// %s returns the translation for %q.\n", methodName, k.key)
			fmt.Fprintf(&b, "func (t *T) %s(%s) string {\n", methodName, strings.Join(paramList, ", "))
			fmt.Fprintf(&b, "\treturn t.tr.T(%q, map[string]any{%s})\n", k.key, strings.Join(mapEntries, ", "))
			b.WriteString("}\n\n")
		default:
			fmt.Fprintf(&b, "// %s returns the translation for %q.\n", methodName, k.key)
			fmt.Fprintf(&b, "func (t *T) %s() string {\n", methodName)
			fmt.Fprintf(&b, "\treturn t.tr.T(%q)\n", k.key)
			b.WriteString("}\n\n")
		}
	}

	return b.String()
}

// keyToMethodName converts a dot-separated key like "home.items_count" into
// a PascalCase Go method name like "HomeItemsCount".
func keyToMethodName(key string) string {
	parts := strings.FieldsFunc(key, func(r rune) bool {
		return r == '.' || r == '_' || r == '-'
	})
	var b strings.Builder
	for _, p := range parts {
		if p == "" {
			continue
		}
		runes := []rune(p)
		runes[0] = unicode.ToUpper(runes[0])
		b.WriteString(string(runes))
	}
	result := b.String()
	if len(result) > 0 && result[0] >= '0' && result[0] <= '9' {
		return "X" + result
	}
	return result
}

func toLowerFirst(s string) string {
	if s == "" {
		return s
	}
	runes := []rune(s)
	runes[0] = unicode.ToLower(runes[0])
	return string(runes)
}
