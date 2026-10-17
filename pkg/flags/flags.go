// Package flags provides feature flags stored in the app's own database:
// typed flags declared in code, per-user evaluation with an allow-list,
// roles and sticky percentage rollout, and an in-memory cache kept fresh by
// database change listeners. See docs/proposed-features/feature-flags.md.
package flags

import (
	"context"
	"encoding/json"
	"fmt"
	"maps"
	"regexp"
	"sort"
	"sync"
)

// Type names the value type of a flag.
type Type string

const (
	TypeBool   Type = "bool"
	TypeInt    Type = "int"
	TypeString Type = "string"
	TypeJSON   Type = "json"
)

// Flag is a typed flag declaration. It holds only the key and the code
// default; live values come from the Client carried on the context.
type Flag[T any] struct {
	key string
	typ Type
	def T
	// defJSON is def encoded once, so JSON flags can hand out fresh copies.
	defJSON json.RawMessage
}

// Definition describes a declared flag, for admin pages and dev actions.
type Definition struct {
	Key     string
	Type    Type
	Default json.RawMessage
}

// decl is a registered flag, with the type-erased pieces the client needs.
type decl struct {
	Definition
	def    any
	decode func(json.RawMessage) (any, error)
}

var (
	regMu    sync.RWMutex
	registry = map[string]*decl{}
)

func lookup(key string) (*decl, bool) {
	regMu.RLock()
	defer regMu.RUnlock()
	d, ok := registry[key]
	return d, ok
}

// validKey keeps keys usable as URL path segments and log attribute names.
var validKey = regexp.MustCompile(`^[a-z0-9][a-z0-9_.-]*$`)

func register[T any](key string, typ Type, def T) *Flag[T] {
	if !validKey.MatchString(key) {
		panic(fmt.Sprintf("flags: key %q must match %s", key, validKey))
	}
	b, err := json.Marshal(def)
	if err != nil {
		b = []byte("null")
	}
	d := &decl{
		Key: key, Type: typ, Default: b,
		def: def,
		decode: func(raw json.RawMessage) (any, error) {
			var v T
			err := json.Unmarshal(raw, &v)
			return v, err
		},
	}
	regMu.Lock()
	defer regMu.Unlock()
	if _, dup := registry[key]; dup {
		panic(fmt.Sprintf("flags: key %q declared twice", key))
	}
	registry[key] = d
	return &Flag[T]{key: key, typ: typ, def: def, defJSON: b}
}

// Bool declares a bool flag. Like every declaration, it panics if key is
// already declared or is not lowercase letters, digits, '_', '.' or '-'.
func Bool(key string, def bool) *Flag[bool] { return register(key, TypeBool, def) }

// Int declares an int flag. It panics if key is already declared.
func Int(key string, def int) *Flag[int] { return register(key, TypeInt, def) }

// String declares a string flag. It panics if key is already declared.
func String(key string, def string) *Flag[string] { return register(key, TypeString, def) }

// JSON declares a flag whose value is any JSON-decodable T. It panics if key
// is already declared. Each Get decodes a fresh T, so callers may mutate it.
func JSON[T any](key string, def T) *Flag[T] { return register(key, TypeJSON, def) }

// Key returns the flag's key.
func (f *Flag[T]) Key() string { return f.key }

// Default returns the code default (a fresh copy for JSON flags).
func (f *Flag[T]) Default() T { return f.fallback() }

// fallback returns the default; JSON flags get a fresh decode so callers can
// mutate maps, slices and pointers without changing the declared default.
func (f *Flag[T]) fallback() T {
	if f.typ == TypeJSON {
		var t T
		if json.Unmarshal(f.defJSON, &t) == nil {
			return t
		}
	}
	return f.def
}

// Get returns the flag's value for the user on ctx. It never errors: with no
// Client on ctx, a disabled rule, or a user who doesn't match, it returns the
// code default.
func (f *Flag[T]) Get(ctx context.Context) T {
	if v, ok := overrides(ctx)[f.key]; ok {
		if t, ok := v.(T); ok {
			return t
		}
	}
	if sc, ok := ctx.Value(scopeKey{}).(scope); ok {
		s := sc.c.snapshot()
		if v, ok := s.value(sc, f.key); ok {
			if f.typ == TypeJSON {
				// Decode a fresh T from the same snapshot: the cached value may
				// hold maps or slices shared by every request.
				var t T
				if json.Unmarshal(s.rules[f.key].Value, &t) == nil {
					return t
				}
				return f.fallback()
			}
			if t, ok := v.(T); ok {
				return t
			}
		}
	}
	return f.fallback()
}

// Definitions returns every declared flag, sorted by key.
func Definitions() []Definition {
	regMu.RLock()
	out := make([]Definition, 0, len(registry))
	for _, d := range registry {
		out = append(out, d.Definition)
	}
	regMu.RUnlock()
	sort.Slice(out, func(i, j int) bool { return out[i].Key < out[j].Key })
	return out
}

// Override returns a ctx on which f always evaluates to v. For tests.
func Override[T any](ctx context.Context, f *Flag[T], v T) context.Context {
	old := overrides(ctx)
	m := make(map[string]any, len(old)+1)
	maps.Copy(m, old)
	m[f.key] = v
	return context.WithValue(ctx, overrideKey{}, m)
}
