package flags

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"maps"
	"slices"
	"sort"
	"sync"
	"sync/atomic"
	"time"
)

// Rule is the stored state of one flag. When Enabled and the user matches
// (in Subjects, has one of Roles, or hashes under Percent), Get returns
// Value; otherwise the code default.
type Rule struct {
	Key      string
	Enabled  bool
	Value    json.RawMessage
	Subjects []string
	Roles    []string
	Percent  int // 0–100
}

// Store persists rules. The app implements it over its own table.
type Store interface {
	List(ctx context.Context) ([]Rule, error)
	Save(ctx context.Context, r Rule) error
}

// Who identifies the user flags are evaluated for.
type Who struct {
	SubjectID string
	Roles     []string
}

// Option configures a Client.
type Option func(*Client)

// Client holds the in-memory rules and keeps them fresh.
type Client struct {
	store        Store
	log          *slog.Logger
	pollInterval time.Duration
	anonCookie   string
	// directURL, set by WithDirectURL, makes the Postgres watcher open its
	// own connection instead of using the pool.
	directURL string
	// watch, when set by WithPostgres or WithSQLite, runs until ctx ends and
	// calls reload whenever the table may have changed.
	watch func(ctx context.Context, c *Client)

	snap atomic.Pointer[snapshot] // read lock-free
	mu   sync.Mutex               // serialises snapshot writers
}

// snapshot is an immutable compiled view of the rules. vals holds the decoded
// value of each rule whose key is declared and whose value decoded.
type snapshot struct {
	rules map[string]Rule
	vals  map[string]any
}

func (c *Client) snapshot() *snapshot {
	if s := c.snap.Load(); s != nil {
		return s
	}
	return &snapshot{}
}

// decodeValue decodes a rule's value into its declared type. An empty or
// null value is not an error: a bool flag reads it as true (enabling a bool
// flag means "on"), any other type serves the code default.
func decodeValue(d *decl, raw json.RawMessage) (v any, ok bool, err error) {
	if s := string(raw); s == "" || s == "null" {
		if d.Type == TypeBool {
			return true, true, nil
		}
		return nil, false, nil
	}
	v, err = d.decode(raw)
	return v, err == nil, err
}

func compile(rules []Rule, log *slog.Logger) *snapshot {
	s := &snapshot{rules: make(map[string]Rule, len(rules)), vals: map[string]any{}}
	for _, r := range rules {
		s.rules[r.Key] = r
		d, declared := lookup(r.Key)
		if !declared {
			continue
		}
		v, ok, err := decodeValue(d, r.Value)
		if err != nil {
			log.Warn("flags: stored value does not decode, serving code default",
				"key", r.Key, "type", string(d.Type), "error", err)
		}
		if ok {
			s.vals[r.Key] = v
		}
	}
	return s
}

// New returns a Client over store. Call Start before serving.
func New(store Store, opts ...Option) *Client {
	c := &Client{store: store}
	for _, o := range opts {
		o(c)
	}
	if c.log == nil {
		c.log = slog.Default()
	}
	if c.pollInterval <= 0 {
		c.pollInterval = 5 * time.Minute
	}
	return c
}

// WithLogger sets the logger for warnings. Default slog.Default().
func WithLogger(l *slog.Logger) Option { return func(c *Client) { c.log = l } }

// WithPollInterval sets the safety-net full reload interval. Default 5m.
func WithPollInterval(d time.Duration) Option { return func(c *Client) { c.pollInterval = d } }

// WithAnonymousCookie makes the middleware set a random first-party ID
// cookie with this name for logged-out visitors, so percentage rollout
// applies to them. The cookie is likely not "strictly necessary" under
// ePrivacy: the app may need consent before enabling it.
func WithAnonymousCookie(name string) Option { return func(c *Client) { c.anonCookie = name } }

// Start loads all rules and starts the poll and the watcher until ctx ends.
// A load error is returned, but the client stays usable: it serves code
// defaults and keeps retrying.
func (c *Client) Start(ctx context.Context) error {
	err := c.reload(ctx)
	if err != nil {
		c.log.Warn("flags: initial load failed, serving code defaults", "error", err)
	}
	go func() {
		t := time.NewTicker(c.pollInterval)
		defer t.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case <-t.C:
				if err := c.reload(ctx); err != nil {
					c.log.Warn("flags: reload failed", "error", err)
				}
			}
		}
	}()
	if c.watch != nil {
		go c.watch(ctx, c)
	}
	return err
}

// ErrInvalid wraps every validation error from Set. Its messages are safe to
// show to the person editing the rule; store errors are not.
var ErrInvalid = errors.New("invalid flag rule")

// Set validates r against the declared flag, saves it and updates the local
// cache immediately. Validation errors wrap ErrInvalid.
func (c *Client) Set(ctx context.Context, r Rule) error {
	d, ok := lookup(r.Key)
	if !ok {
		return fmt.Errorf("%w: unknown key %q", ErrInvalid, r.Key)
	}
	if r.Percent < 0 || r.Percent > 100 {
		return fmt.Errorf("%w: percent %d out of range 0-100", ErrInvalid, r.Percent)
	}
	v, hasVal, err := decodeValue(d, r.Value)
	if err != nil {
		if !json.Valid(r.Value) {
			return fmt.Errorf("%w: value for %q is not valid JSON", ErrInvalid, r.Key)
		}
		return fmt.Errorf("%w: value for %q does not match the flag's type (%s)", ErrInvalid, r.Key, d.Type)
	}
	// Hold mu across Save so a concurrent reload can't swap in a List
	// taken before this write.
	c.mu.Lock()
	defer c.mu.Unlock()
	if err := c.store.Save(ctx, r); err != nil {
		return err
	}
	old := c.snapshot()
	s := &snapshot{rules: make(map[string]Rule, len(old.rules)+1), vals: make(map[string]any, len(old.vals)+1)}
	maps.Copy(s.rules, old.rules)
	maps.Copy(s.vals, old.vals)
	s.rules[r.Key] = cloneRule(r)
	delete(s.vals, r.Key)
	if hasVal {
		s.vals[r.Key] = v
	}
	c.snap.Store(s)
	return nil
}

// cloneRule copies r's slices so callers can't mutate the shared snapshot.
func cloneRule(r Rule) Rule {
	r.Value = slices.Clone(r.Value)
	r.Subjects = slices.Clone(r.Subjects)
	r.Roles = slices.Clone(r.Roles)
	return r
}

// Rules returns copies of the current rules, sorted by key.
func (c *Client) Rules() []Rule {
	s := c.snapshot()
	out := make([]Rule, 0, len(s.rules))
	for _, r := range s.rules {
		out = append(out, cloneRule(r))
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Key < out[j].Key })
	return out
}

// For returns a ctx evaluating flags through c for w. For background jobs.
func (c *Client) For(ctx context.Context, w Who) context.Context {
	return withScope(ctx, c, w, "")
}

// reload replaces the cache with the store's current rules. It holds mu
// across List, so it can't overwrite a concurrent Set with older rules.
func (c *Client) reload(ctx context.Context) error {
	c.mu.Lock()
	defer c.mu.Unlock()
	rules, err := c.store.List(ctx)
	if err != nil {
		return err
	}
	c.snap.Store(compile(rules, c.log))
	return nil
}
