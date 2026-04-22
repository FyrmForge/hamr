package scaffold

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// withReleaseURL points the package at an httptest server for the duration of
// a test and restores the original on cleanup.
func withReleaseURL(t *testing.T, url string) {
	t.Helper()
	orig := githubLatestReleaseURL
	githubLatestReleaseURL = url
	t.Cleanup(func() { githubLatestReleaseURL = orig })
}

func TestFetchLatestReleaseSuccess(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		assert.Equal(t, "application/vnd.github+json", r.Header.Get("Accept"))
		_, _ = fmt.Fprintln(w, `{"tag_name":"v1.2.3"}`)
	}))
	defer srv.Close()
	withReleaseURL(t, srv.URL)

	v, err := FetchLatestRelease(context.Background())
	require.NoError(t, err)
	assert.Equal(t, Version{1, 2, 3}, v)
}

func TestFetchLatestReleaseNoPrefix(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = fmt.Fprintln(w, `{"tag_name":"0.5.1"}`)
	}))
	defer srv.Close()
	withReleaseURL(t, srv.URL)

	v, err := FetchLatestRelease(context.Background())
	require.NoError(t, err)
	assert.Equal(t, Version{0, 5, 1}, v)
}

func TestFetchLatestReleaseNon200(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusServiceUnavailable)
	}))
	defer srv.Close()
	withReleaseURL(t, srv.URL)

	_, err := FetchLatestRelease(context.Background())
	require.Error(t, err)
	assert.Contains(t, err.Error(), "unexpected status 503")
}

func TestFetchLatestReleaseBadJSON(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = fmt.Fprintln(w, `not json`)
	}))
	defer srv.Close()
	withReleaseURL(t, srv.URL)

	_, err := FetchLatestRelease(context.Background())
	require.Error(t, err)
	assert.Contains(t, strings.ToLower(err.Error()), "decode")
}

func TestFetchLatestReleaseInvalidTag(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = fmt.Fprintln(w, `{"tag_name":"not-a-version"}`)
	}))
	defer srv.Close()
	withReleaseURL(t, srv.URL)

	_, err := FetchLatestRelease(context.Background())
	require.Error(t, err)
	assert.Contains(t, err.Error(), "parse tag")
}
