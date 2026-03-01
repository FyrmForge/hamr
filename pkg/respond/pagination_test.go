package respond

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/labstack/echo/v4"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func paginationContext(query string) echo.Context {
	e := echo.New()
	r := httptest.NewRequest(http.MethodGet, "/?"+query, nil)
	w := httptest.NewRecorder()
	return e.NewContext(r, w)
}

// ---------------------------------------------------------------------------
// ParsePagination
// ---------------------------------------------------------------------------

func TestParsePagination_defaults(t *testing.T) {
	c := paginationContext("")
	page, size := ParsePagination(c, 20)
	assert.Equal(t, 1, page)
	assert.Equal(t, 20, size)
}

func TestParsePagination_valid(t *testing.T) {
	c := paginationContext("page=3&size=25")
	page, size := ParsePagination(c, 20)
	assert.Equal(t, 3, page)
	assert.Equal(t, 25, size)
}

func TestParsePagination_negativePage(t *testing.T) {
	c := paginationContext("page=-5")
	page, _ := ParsePagination(c, 20)
	assert.Equal(t, 1, page)
}

func TestParsePagination_zeroSize(t *testing.T) {
	c := paginationContext("size=0")
	_, size := ParsePagination(c, 20)
	assert.Equal(t, 20, size)
}

func TestParsePagination_exceeds100(t *testing.T) {
	c := paginationContext("size=200")
	_, size := ParsePagination(c, 20)
	assert.Equal(t, 100, size)
}

func TestParsePagination_invalidStrings(t *testing.T) {
	c := paginationContext("page=abc&size=xyz")
	page, size := ParsePagination(c, 20)
	assert.Equal(t, 1, page)
	assert.Equal(t, 20, size)
}

// ---------------------------------------------------------------------------
// NewPage
// ---------------------------------------------------------------------------

func TestNewPage_firstPage(t *testing.T) {
	p := NewPage(1, 10, 25)
	assert.Equal(t, 1, p.Number)
	assert.Equal(t, 10, p.Size)
	assert.Equal(t, 25, p.Total)
	assert.Equal(t, 3, p.TotalPages)
	assert.True(t, p.HasNext)
	assert.False(t, p.HasPrev)
}

func TestNewPage_lastPage(t *testing.T) {
	p := NewPage(3, 10, 25)
	assert.False(t, p.HasNext)
	assert.True(t, p.HasPrev)
}

func TestNewPage_singlePage(t *testing.T) {
	p := NewPage(1, 10, 5)
	assert.Equal(t, 1, p.TotalPages)
	assert.False(t, p.HasNext)
	assert.False(t, p.HasPrev)
}

func TestNewPage_empty(t *testing.T) {
	p := NewPage(1, 10, 0)
	assert.Equal(t, 0, p.TotalPages)
	assert.Equal(t, 0, p.Total)
	assert.False(t, p.HasNext)
	assert.False(t, p.HasPrev)
}

func TestNewPage_exactFit(t *testing.T) {
	p := NewPage(1, 10, 10)
	assert.Equal(t, 1, p.TotalPages)
}

func TestNewPage_partial(t *testing.T) {
	p := NewPage(1, 10, 11)
	assert.Equal(t, 2, p.TotalPages)
}

func TestNewPage_zeroSize(t *testing.T) {
	assert.NotPanics(t, func() {
		p := NewPage(1, 0, 10)
		assert.Equal(t, 0, p.TotalPages)
	})
}

func TestPagedResponse_jsonSerialization(t *testing.T) {
	resp := PagedResponse[string]{
		Data: []string{"a", "b"},
		Page: NewPage(1, 10, 2),
	}

	b, err := json.Marshal(resp)
	require.NoError(t, err)

	var out map[string]json.RawMessage
	require.NoError(t, json.Unmarshal(b, &out))
	assert.Contains(t, string(out["data"]), `"a"`)
	assert.Contains(t, string(out["page"]), `"number"`)
}
