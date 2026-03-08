package ctx_test

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/FyrmForge/hamr/pkg/ctx"
	"github.com/labstack/echo/v4"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func newEchoContext() echo.Context {
	e := echo.New()
	r := httptest.NewRequest(http.MethodGet, "/", nil)
	w := httptest.NewRecorder()
	return e.NewContext(r, w)
}

// ---------------------------------------------------------------------------
// Key
// ---------------------------------------------------------------------------

func TestNewKey_String(t *testing.T) {
	k := ctx.NewKey[string]("my_key")
	assert.Equal(t, "my_key", k.String())
}

// ---------------------------------------------------------------------------
// Set / Get
// ---------------------------------------------------------------------------

func TestSetAndGet_string(t *testing.T) {
	c := newEchoContext()
	k := ctx.NewKey[string]("name")
	ctx.Set(c, k, "alice")

	v, ok := ctx.Get(c, k)
	require.True(t, ok)
	assert.Equal(t, "alice", v)
}

func TestSetAndGet_int(t *testing.T) {
	c := newEchoContext()
	k := ctx.NewKey[int]("count")
	ctx.Set(c, k, 42)

	v, ok := ctx.Get(c, k)
	require.True(t, ok)
	assert.Equal(t, 42, v)
}

func TestSetAndGet_struct(t *testing.T) {
	type user struct{ Name string }
	c := newEchoContext()
	k := ctx.NewKey[user]("user")
	ctx.Set(c, k, user{Name: "bob"})

	v, ok := ctx.Get(c, k)
	require.True(t, ok)
	assert.Equal(t, "bob", v.Name)
}

func TestGet_missing(t *testing.T) {
	c := newEchoContext()
	k := ctx.NewKey[string]("absent")

	v, ok := ctx.Get(c, k)
	assert.False(t, ok)
	assert.Equal(t, "", v)
}

func TestGet_wrongType(t *testing.T) {
	c := newEchoContext()
	kStr := ctx.NewKey[string]("val")
	kInt := ctx.NewKey[int]("val")

	ctx.Set(c, kStr, "hello")

	v, ok := ctx.Get(c, kInt)
	assert.False(t, ok)
	assert.Equal(t, 0, v)
}

// ---------------------------------------------------------------------------
// MustGet
// ---------------------------------------------------------------------------

func TestMustGet_success(t *testing.T) {
	c := newEchoContext()
	k := ctx.NewKey[string]("name")
	ctx.Set(c, k, "alice")

	assert.Equal(t, "alice", ctx.MustGet(c, k))
}

func TestMustGet_panicsOnMissing(t *testing.T) {
	c := newEchoContext()
	k := ctx.NewKey[string]("missing")

	assert.Panics(t, func() { ctx.MustGet(c, k) })
}

// ---------------------------------------------------------------------------
// Overwrite
// ---------------------------------------------------------------------------

func TestOverwrite(t *testing.T) {
	c := newEchoContext()
	k := ctx.NewKey[string]("color")
	ctx.Set(c, k, "red")
	ctx.Set(c, k, "blue")

	v, ok := ctx.Get(c, k)
	require.True(t, ok)
	assert.Equal(t, "blue", v)
}

// ---------------------------------------------------------------------------
// Multiple keys
// ---------------------------------------------------------------------------

func TestMultipleKeys(t *testing.T) {
	c := newEchoContext()
	k1 := ctx.NewKey[string]("a")
	k2 := ctx.NewKey[string]("b")
	ctx.Set(c, k1, "alpha")
	ctx.Set(c, k2, "beta")

	v1, _ := ctx.Get(c, k1)
	v2, _ := ctx.Get(c, k2)
	assert.Equal(t, "alpha", v1)
	assert.Equal(t, "beta", v2)
}

// ---------------------------------------------------------------------------
// GetAs
// ---------------------------------------------------------------------------

func TestGetAs_success(t *testing.T) {
	type user struct{ Name string }
	c := newEchoContext()
	c.Set("subject", &user{Name: "alice"})

	got, ok := ctx.GetAs[*user](c, ctx.SubjectKey)
	require.True(t, ok)
	assert.Equal(t, "alice", got.Name)
}

func TestGetAs_wrongType(t *testing.T) {
	c := newEchoContext()
	c.Set("subject", "not-a-struct")

	type user struct{ Name string }
	got, ok := ctx.GetAs[*user](c, ctx.SubjectKey)
	assert.False(t, ok)
	assert.Nil(t, got)
}

func TestGetAs_missing(t *testing.T) {
	c := newEchoContext()

	got, ok := ctx.GetAs[string](c, ctx.SubjectKey)
	assert.False(t, ok)
	assert.Equal(t, "", got)
}

func TestMustGetAs_success(t *testing.T) {
	type user struct{ Name string }
	c := newEchoContext()
	c.Set("subject", &user{Name: "bob"})

	got := ctx.MustGetAs[*user](c, ctx.SubjectKey)
	assert.Equal(t, "bob", got.Name)
}

func TestMustGetAs_panicsOnMissing(t *testing.T) {
	c := newEchoContext()
	assert.Panics(t, func() {
		ctx.MustGetAs[string](c, ctx.SubjectKey)
	})
}

func TestMustGetAs_panicsOnWrongType(t *testing.T) {
	c := newEchoContext()
	c.Set("subject", 42)
	assert.Panics(t, func() {
		ctx.MustGetAs[string](c, ctx.SubjectKey)
	})
}

// ---------------------------------------------------------------------------
// Pre-defined keys
// ---------------------------------------------------------------------------

func TestPreDefinedKeys(t *testing.T) {
	assert.Equal(t, "subject_id", ctx.SubjectIDKey.String())
	assert.Equal(t, "subject", ctx.SubjectKey.String())
	assert.Equal(t, "session", ctx.SessionKey.String())
	assert.Equal(t, "request_id", ctx.RequestIDKey.String())
	assert.Equal(t, "flash", ctx.FlashKey.String())
}
