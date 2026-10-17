package flags

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"maps"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/FyrmForge/hamr/pkg/logging"
	"github.com/labstack/echo/v4"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func raw(s string) json.RawMessage { return json.RawMessage(s) }

// isolate restores the global registry after t, so tests can declare flags
// in-function and still pass under -count=N.
func isolate(t *testing.T) {
	regMu.Lock()
	saved := maps.Clone(registry)
	regMu.Unlock()
	t.Cleanup(func() {
		regMu.Lock()
		registry = saved
		regMu.Unlock()
	})
}

func started(t *testing.T, opts []Option, rules ...Rule) *Client {
	t.Helper()
	c := New(NewMemStore(rules...), opts...)
	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)
	require.NoError(t, c.Start(ctx))
	return c
}

func TestMatching(t *testing.T) {
	isolate(t)
	f := Bool("t_match", false)
	rule := func(mod func(*Rule)) Rule {
		r := Rule{Key: "t_match", Enabled: true, Value: raw("true")}
		mod(&r)
		return r
	}
	cases := []struct {
		name string
		rule Rule
		who  Who
		anon string
		want bool
	}{
		{"allow-list", rule(func(r *Rule) { r.Subjects = []string{"u1"} }), Who{SubjectID: "u1"}, "", true},
		{"not in allow-list", rule(func(r *Rule) { r.Subjects = []string{"u1"} }), Who{SubjectID: "u2"}, "", false},
		{"role", rule(func(r *Rule) { r.Roles = []string{"admin"} }), Who{SubjectID: "u2", Roles: []string{"user", "admin"}}, "", true},
		{"wrong role", rule(func(r *Rule) { r.Roles = []string{"admin"} }), Who{Roles: []string{"user"}}, "", false},
		{"percent 0", rule(func(r *Rule) {}), Who{SubjectID: "u1"}, "", false},
		{"percent 100", rule(func(r *Rule) { r.Percent = 100 }), Who{SubjectID: "u1"}, "", true},
		{"percent 100 anonymous no cookie", rule(func(r *Rule) { r.Percent = 100 }), Who{}, "", true},
		{"percent 50 anonymous no cookie", rule(func(r *Rule) { r.Percent = 99 }), Who{}, "", false},
		{"disabled", rule(func(r *Rule) { r.Enabled = false; r.Percent = 100 }), Who{SubjectID: "u1"}, "", false},
		{"anonymous cookie id hashed", rule(func(r *Rule) { r.Percent = 100 }), Who{}, "abc", true},
		{"anonymous id not in allow-list", rule(func(r *Rule) { r.Subjects = []string{"abc"} }), Who{}, "abc", false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			c := New(NewMemStore(tc.rule))
			require.NoError(t, c.reload(context.Background()))
			ctx := withScope(context.Background(), c, tc.who, tc.anon)
			assert.Equal(t, tc.want, f.Get(ctx))
		})
	}
	assert.False(t, f.Get(context.Background()), "no client on ctx")
}

func TestPercentAnonymousUsesCookieID(t *testing.T) {
	isolate(t)
	f := Bool("t_anon_pct", false)
	c := New(NewMemStore(Rule{Key: "t_anon_pct", Enabled: true, Value: raw("true"), Percent: 50}))
	require.NoError(t, c.reload(context.Background()))
	on, off := 0, 0
	for i := range 200 {
		if f.Get(withScope(context.Background(), c, Who{}, fmt.Sprint("anon", i))) {
			on++
		} else {
			off++
		}
	}
	assert.Positive(t, on)
	assert.Positive(t, off)
}

func TestBucketStickyAndDistribution(t *testing.T) {
	isolate(t)
	assert.Equal(t, bucket("k", "user-1"), bucket("k", "user-1"))
	assert.NotEqual(t, bucket("ab", "c"), bucket("a", "bc")) // separator; fixed inputs
	in := 0
	for i := range 10000 {
		if bucket("t_dist", fmt.Sprintf("user-%d", i)) < 10 {
			in++
		}
	}
	assert.InDelta(t, 1000, in, 150)
}

func TestBadStoredValueServesDefaultWarnsOnce(t *testing.T) {
	isolate(t)
	f := Int("t_bad", 7)
	var buf bytes.Buffer
	log := slog.New(slog.NewTextHandler(&buf, nil))
	c := New(NewMemStore(Rule{Key: "t_bad", Enabled: true, Value: raw(`"nope"`), Percent: 100}), WithLogger(log))
	require.NoError(t, c.reload(context.Background()))
	ctx := c.For(context.Background(), Who{SubjectID: "u"})
	for range 5 {
		assert.Equal(t, 7, f.Get(ctx))
	}
	assert.Equal(t, 1, strings.Count(buf.String(), "t_bad"))
	assert.Len(t, c.Rules(), 1)
}

func TestUndeclaredRuleKeptButIgnored(t *testing.T) {
	isolate(t)
	c := New(NewMemStore(Rule{Key: "t_undeclared", Enabled: true, Value: raw("true"), Percent: 100}))
	require.NoError(t, c.reload(context.Background()))
	assert.Len(t, c.Rules(), 1)
}

func TestSetValidation(t *testing.T) {
	isolate(t)
	Bool("t_set_val", false)
	c := started(t, nil)
	ctx := context.Background()
	assert.Error(t, c.Set(ctx, Rule{Key: "t_unknown_key", Value: raw("true")}))
	assert.Error(t, c.Set(ctx, Rule{Key: "t_set_val", Value: raw(`"x"`)}))
	assert.Error(t, c.Set(ctx, Rule{Key: "t_set_val", Value: raw("true"), Percent: 101}))
	assert.Error(t, c.Set(ctx, Rule{Key: "t_set_val", Value: raw("true"), Percent: -1}))
	assert.NoError(t, c.Set(ctx, Rule{Key: "t_set_val", Value: raw("true"), Percent: 100}))
}

type saveFailStore struct{ Store }

func (saveFailStore) Save(context.Context, Rule) error { return errors.New("db down") }

func TestSetErrInvalid(t *testing.T) {
	isolate(t)
	Bool("t_set_inv", false)
	ctx := context.Background()
	c := started(t, nil)
	assert.ErrorIs(t, c.Set(ctx, Rule{Key: "t_nope", Value: raw("true")}), ErrInvalid)
	assert.ErrorIs(t, c.Set(ctx, Rule{Key: "t_set_inv", Value: raw("true"), Percent: 101}), ErrInvalid)
	assert.ErrorIs(t, c.Set(ctx, Rule{Key: "t_set_inv", Value: raw(`"x"`)}), ErrInvalid)

	fc := New(saveFailStore{NewMemStore()})
	err := fc.Set(ctx, Rule{Key: "t_set_inv", Value: raw("true"), Percent: 100})
	require.Error(t, err)
	assert.NotErrorIs(t, err, ErrInvalid)
}

func TestSetValueErrorMessages(t *testing.T) {
	isolate(t)
	Bool("t_set_msg", false)
	JSON("t_set_msg_json", map[string]any{})
	c := started(t, nil)
	ctx := context.Background()
	err := c.Set(ctx, Rule{Key: "t_set_msg", Value: raw(`{nope`)})
	assert.ErrorIs(t, err, ErrInvalid)
	assert.ErrorContains(t, err, `value for "t_set_msg" is not valid JSON`)
	err = c.Set(ctx, Rule{Key: "t_set_msg", Value: raw(`"x"`)})
	assert.ErrorIs(t, err, ErrInvalid)
	assert.ErrorContains(t, err, `value for "t_set_msg" does not match the flag's type (bool)`)
	err = c.Set(ctx, Rule{Key: "t_set_msg_json", Value: raw(`[1]`)})
	assert.ErrorIs(t, err, ErrInvalid)
	assert.ErrorContains(t, err, "does not match the flag's type")
}

func TestSetUpdatesCacheWithoutReload(t *testing.T) {
	isolate(t)
	f := String("t_set_cache", "def")
	store := NewMemStore()
	c := New(store)
	require.NoError(t, c.Start(context.Background()))
	ctx := c.For(context.Background(), Who{SubjectID: "u"})
	assert.Equal(t, "def", f.Get(ctx))
	require.NoError(t, c.Set(context.Background(), Rule{Key: "t_set_cache", Enabled: true, Value: raw(`"new"`), Percent: 100}))
	assert.Equal(t, "new", f.Get(ctx))
	rs, _ := store.List(context.Background())
	assert.Len(t, rs, 1)
	assert.Len(t, c.Rules(), 1)
}

type failStore struct{ Store }

func (failStore) List(context.Context) ([]Rule, error) { return nil, errors.New("down") }

func TestStartLoadErrorStaysUsable(t *testing.T) {
	isolate(t)
	f := Bool("t_start_err", true)
	c := New(failStore{NewMemStore()})
	ctx := t.Context()
	assert.Error(t, c.Start(ctx))
	assert.True(t, f.Get(c.For(ctx, Who{})))
}

func TestStartRunsWatch(t *testing.T) {
	isolate(t)
	done := make(chan struct{})
	c := New(NewMemStore(), func(c *Client) { c.watch = func(context.Context, *Client) { close(done) } })
	ctx := t.Context()
	require.NoError(t, c.Start(ctx))
	<-done
}

func TestOverrideAndStack(t *testing.T) {
	isolate(t)
	a, b := Bool("t_ov_a", false), Int("t_ov_b", 1)
	ctx := Override(context.Background(), a, true)
	ctx2 := Override(ctx, b, 9)
	assert.True(t, a.Get(ctx2))
	assert.Equal(t, 9, b.Get(ctx2))
	assert.Equal(t, 1, b.Get(ctx), "parent ctx unchanged")
	// override wins over client
	c := started(t, nil, Rule{Key: "t_ov_a", Enabled: true, Value: raw("false"), Percent: 100})
	assert.True(t, a.Get(Override(c.For(context.Background(), Who{}), a, true)))
}

func TestJSONFlag(t *testing.T) {
	isolate(t)
	type cfg struct{ Color string }
	f := JSON("t_json", cfg{Color: "red"})
	c := started(t, nil, Rule{Key: "t_json", Enabled: true, Value: raw(`{"Color":"blue"}`), Percent: 100})
	assert.Equal(t, cfg{Color: "blue"}, f.Get(c.For(context.Background(), Who{})))
}

func TestJSONFlagGetReturnsFreshCopy(t *testing.T) {
	isolate(t)
	f := JSON("t_json_map", map[string]int{})
	c := started(t, nil, Rule{Key: "t_json_map", Enabled: true, Value: raw(`{"pro":10}`), Percent: 100})
	ctx := c.For(context.Background(), Who{})
	f.Get(ctx)["pro"] = 0
	assert.Equal(t, 10, f.Get(ctx)["pro"])
}

func TestJSONFlagDefaultIsNeverShared(t *testing.T) {
	isolate(t)
	f := JSON("t_json_def", map[string]int{"pro": 10})
	f.Get(context.Background())["pro"] = 0
	assert.Equal(t, 10, f.Get(context.Background())["pro"])

	c := started(t, nil, Rule{Key: "t_json_def", Enabled: false, Value: raw(`{"pro":1}`), Percent: 100})
	ctx := c.For(context.Background(), Who{})
	f.Get(ctx)["pro"] = 0
	assert.Equal(t, 10, f.Get(ctx)["pro"])

	f.Default()["pro"] = 0
	assert.Equal(t, 10, f.Default()["pro"])
}

func TestInvalidKeyPanics(t *testing.T) {
	isolate(t)
	for _, k := range []string{"", "a/b", "a?b", "Upper", "_lead"} {
		assert.Panics(t, func() { Bool(k, false) }, k)
	}
}

func TestDuplicatePanics(t *testing.T) {
	isolate(t)
	Bool("t_dup", false)
	assert.Panics(t, func() { String("t_dup", "") })
}

func TestDefinitionsSorted(t *testing.T) {
	isolate(t)
	Int("t_defs_b", 2)
	Bool("t_defs_a", true)
	var keys []string
	var a Definition
	for _, d := range Definitions() {
		keys = append(keys, d.Key)
		if d.Key == "t_defs_a" {
			a = d
		}
	}
	assert.IsIncreasing(t, keys)
	assert.Equal(t, TypeBool, a.Type)
	assert.JSONEq(t, "true", string(a.Default))
}

func TestMemStoreSorted(t *testing.T) {
	isolate(t)
	s := NewMemStore(Rule{Key: "b"}, Rule{Key: "a"})
	require.NoError(t, s.Save(context.Background(), Rule{Key: "a", Percent: 5}))
	rs, _ := s.List(context.Background())
	require.Len(t, rs, 2)
	assert.Equal(t, "a", rs[0].Key)
	assert.Equal(t, 5, rs[0].Percent)
}

func serve(c *Client, who Who, req *http.Request, h echo.HandlerFunc) *httptest.ResponseRecorder {
	e := echo.New()
	rec := httptest.NewRecorder()
	ec := e.NewContext(req, rec)
	_ = c.Middleware(func(echo.Context) Who { return who })(h)(ec)
	return rec
}

func TestMiddlewareWhoLogAndCookie(t *testing.T) {
	isolate(t)
	f := Bool("t_mw", false)
	c := started(t, []Option{WithAnonymousCookie("anon")},
		Rule{Key: "t_mw", Enabled: true, Value: raw("true"), Percent: 100})

	var buf bytes.Buffer
	log := slog.New(slog.NewJSONHandler(&buf, nil))
	req := httptest.NewRequest(http.MethodGet, "/", nil)
	req = req.WithContext(logging.WithLogger(req.Context(), log))

	var got bool
	rec := serve(c, Who{}, req, func(ec echo.Context) error {
		ctx := ec.Request().Context()
		got = f.Get(ctx)
		logging.FromContext(ctx).Info("hello")
		return nil
	})
	assert.True(t, got)
	cookies := rec.Result().Cookies()
	require.Len(t, cookies, 1)
	ck := cookies[0]
	assert.Equal(t, "anon", ck.Name)
	assert.Len(t, ck.Value, 32)
	assert.True(t, ck.HttpOnly)
	assert.Equal(t, http.SameSiteLaxMode, ck.SameSite)
	assert.Equal(t, "/", ck.Path)
	assert.Equal(t, 365*24*3600, ck.MaxAge)
	assert.False(t, ck.Secure)

	var line map[string]any
	require.NoError(t, json.Unmarshal(buf.Bytes(), &line))
	g, ok := line["flags"].(map[string]any)
	require.True(t, ok, buf.String())
	assert.Equal(t, true, g["t_mw"])

	// cookie reused: no new Set-Cookie
	req2 := httptest.NewRequest(http.MethodGet, "/", nil)
	req2.AddCookie(ck)
	rec2 := serve(c, Who{}, req2, func(echo.Context) error { return nil })
	assert.Empty(t, rec2.Result().Cookies())
}

func TestMiddlewareNoCookieWhenLoggedInOrNotConfigured(t *testing.T) {
	isolate(t)
	c := started(t, []Option{WithAnonymousCookie("anon")})
	rec := serve(c, Who{SubjectID: "u1", Roles: []string{"admin"}},
		httptest.NewRequest(http.MethodGet, "/", nil), func(ec echo.Context) error {
			sc := ec.Request().Context().Value(scopeKey{}).(scope)
			assert.Equal(t, Who{SubjectID: "u1", Roles: []string{"admin"}}, sc.who)
			return nil
		})
	assert.Empty(t, rec.Result().Cookies())

	c2 := started(t, nil)
	rec = serve(c2, Who{}, httptest.NewRequest(http.MethodGet, "/", nil), func(echo.Context) error { return nil })
	assert.Empty(t, rec.Result().Cookies())
}

func TestNullValue(t *testing.T) {
	isolate(t)
	b := Bool("null_test_bool", false)
	n := Int("null_test_int", 3)
	c := New(NewMemStore(
		Rule{Key: "null_test_bool", Enabled: true, Percent: 100},
		Rule{Key: "null_test_int", Enabled: true, Percent: 100, Value: json.RawMessage("null")},
	))
	require.NoError(t, c.Start(context.Background()))
	ctx := c.For(context.Background(), Who{SubjectID: "u"})
	assert.True(t, b.Get(ctx), "enabled bool with no value is on")
	assert.Equal(t, 3, n.Get(ctx), "non-bool with no value serves the code default")
}

// listGate holds List after reading, so a Set can race the reload.
type listGate struct {
	Store
	read, release chan struct{}
}

func (g *listGate) List(ctx context.Context) ([]Rule, error) {
	rules, err := g.Store.List(ctx)
	close(g.read)
	<-g.release
	return rules, err
}

func TestReloadCannotOverwriteConcurrentSet(t *testing.T) {
	isolate(t)
	f := Int("race_test_int", 0)
	g := &listGate{Store: NewMemStore(Rule{Key: "race_test_int", Enabled: true, Percent: 100, Value: json.RawMessage("1")}),
		read: make(chan struct{}), release: make(chan struct{})}
	c := New(g)
	reloaded := make(chan error)
	go func() { reloaded <- c.reload(context.Background()) }()
	<-g.read // reload holds rules with value 1
	set := make(chan error)
	go func() {
		set <- c.Set(context.Background(), Rule{Key: "race_test_int", Enabled: true, Percent: 100, Value: json.RawMessage("2")})
	}()
	close(g.release)
	require.NoError(t, <-reloaded)
	require.NoError(t, <-set)
	assert.Equal(t, 2, f.Get(c.For(context.Background(), Who{SubjectID: "u"})))
}
