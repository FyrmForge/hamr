package flags

import (
	"context"
	"hash/fnv"
	"slices"
)

type (
	scopeKey    struct{}
	overrideKey struct{}
)

// scope is what travels on ctx: the client, the user, and the ID hashed for
// percentage rollout (SubjectID, or the anonymous cookie ID when logged out).
type scope struct {
	c    *Client
	who  Who
	hash string
}

func overrides(ctx context.Context) map[string]any {
	m, _ := ctx.Value(overrideKey{}).(map[string]any)
	return m
}

func withScope(ctx context.Context, c *Client, w Who, anon string) context.Context {
	h := w.SubjectID
	if h == "" {
		h = anon
	}
	return context.WithValue(ctx, scopeKey{}, scope{c: c, who: w, hash: h})
}

// value returns the rule's decoded value when the rule exists, decoded and
// matches the user.
func (c *Client) value(sc scope, key string) (any, bool) {
	return c.snapshot().value(sc, key)
}

func (s *snapshot) value(sc scope, key string) (any, bool) {
	v, ok := s.vals[key]
	if !ok || !matches(s.rules[key], sc) {
		return nil, false
	}
	return v, true
}

func matches(r Rule, sc scope) bool {
	if !r.Enabled {
		return false
	}
	if sc.who.SubjectID != "" && slices.Contains(r.Subjects, sc.who.SubjectID) {
		return true
	}
	for _, role := range sc.who.Roles {
		if slices.Contains(r.Roles, role) {
			return true
		}
	}
	if r.Percent >= 100 {
		return true
	}
	return sc.hash != "" && bucket(r.Key, sc.hash) < r.Percent
}

// bucket maps key+id to 0-99, stable per (key, id). The separator stops
// "ab"+"c" and "a"+"bc" colliding.
func bucket(key, id string) int {
	h := fnv.New32a()
	h.Write([]byte(key + "\x00" + id))
	return int(h.Sum32() % 100)
}
