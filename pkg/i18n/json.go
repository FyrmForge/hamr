package i18n

import (
	"encoding/json"
	"fmt"
	"os"
	"regexp"
	"slices"
	"sort"
	"strings"
)

// localeMeta holds optional metadata from the _meta key in a locale JSON file.
type localeMeta struct {
	Direction   string `json:"direction"`   // "ltr" or "rtl"
	DisplayName string `json:"displayName"` // e.g. "English"
}

// ValidationError describes a mismatch between locale files.
type ValidationError struct {
	Locale  string
	Key     string
	Message string
}

func (e ValidationError) Error() string {
	return fmt.Sprintf("[%s] %s: %s", e.Locale, e.Key, e.Message)
}

// loadJSON reads and unmarshals a JSON file into a generic map.
func loadJSON(path string) (map[string]any, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("read %s: %w", path, err)
	}
	var out map[string]any
	if err := json.Unmarshal(data, &out); err != nil {
		return nil, fmt.Errorf("parse %s: %w", path, err)
	}
	return out, nil
}

// flattenMessages recursively flattens a nested JSON map into dot-notated
// message entries. Keys starting with "_" (like _meta) are skipped.
func flattenMessages(data map[string]any, prefix string) (map[string]message, error) {
	out := make(map[string]message)
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
			msg, err := newMessage(val)
			if err != nil {
				return nil, fmt.Errorf("key %s: %w", key, err)
			}
			out[key] = msg
		case map[string]any:
			if IsPluralObject(val) {
				pm, err := newPluralMessage(val)
				if err != nil {
					return nil, fmt.Errorf("key %s: %w", key, err)
				}
				out[key] = pm
			} else {
				nested, err := flattenMessages(val, key)
				if err != nil {
					return nil, err
				}
				for nk, nv := range nested {
					out[nk] = nv
				}
			}
		default:
			return nil, fmt.Errorf("key %s: unsupported type %T", key, v)
		}
	}
	return out, nil
}

// extractMeta reads the optional _meta object from a parsed locale file.
func extractMeta(data map[string]any) *localeMeta {
	raw, ok := data["_meta"]
	if !ok {
		return nil
	}
	obj, ok := raw.(map[string]any)
	if !ok {
		return nil
	}
	meta := &localeMeta{}
	if d, ok := obj["direction"].(string); ok {
		meta.Direction = d
	}
	if d, ok := obj["displayName"].(string); ok {
		meta.DisplayName = d
	}
	return meta
}

// IsPluralObject returns true when all keys in obj are valid CLDR plural
// category names and all values are strings.
func IsPluralObject(obj map[string]any) bool {
	if len(obj) == 0 {
		return false
	}
	for k, v := range obj {
		if _, ok := validPluralCategories[k]; !ok {
			return false
		}
		if _, ok := v.(string); !ok {
			return false
		}
	}
	return true
}

// InterpolationVars extracts {{.VarName}} placeholders from a template string.
// Returns a sorted, deduplicated list of variable names.
var interpolationRe = regexp.MustCompile(`\{\{\s*\.(\w+)\s*\}\}`)

func InterpolationVars(s string) []string {
	matches := interpolationRe.FindAllStringSubmatch(s, -1)
	seen := map[string]bool{}
	var vars []string
	for _, m := range matches {
		name := m[1]
		if !seen[name] {
			seen[name] = true
			vars = append(vars, name)
		}
	}
	sort.Strings(vars)
	return vars
}

// validateLocale checks a non-default locale against the default for missing
// keys, extra keys, and mismatched interpolation variables.
func validateLocale(defaultMsgs, localeMsgs map[string]message, localeName string) []ValidationError {
	var errs []ValidationError

	// Check for missing keys.
	for k := range defaultMsgs {
		if _, ok := localeMsgs[k]; !ok {
			errs = append(errs, ValidationError{
				Locale:  localeName,
				Key:     k,
				Message: "missing key (will fall back to default locale)",
			})
		}
	}

	// Check for extra keys and mismatched interpolation.
	for k, lm := range localeMsgs {
		dm, ok := defaultMsgs[k]
		if !ok {
			errs = append(errs, ValidationError{
				Locale:  localeName,
				Key:     k,
				Message: "extra key not present in default locale",
			})
			continue
		}

		defVars := dm.vars()
		locVars := lm.vars()
		if !slices.Equal(defVars, locVars) {
			errs = append(errs, ValidationError{
				Locale:  localeName,
				Key:     k,
				Message: fmt.Sprintf("interpolation mismatch: default has %v, locale has %v", defVars, locVars),
			})
		}
	}

	return errs
}

