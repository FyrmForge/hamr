package generator

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func sha256hex(s string) string {
	h := sha256.Sum256([]byte(s))
	return hex.EncodeToString(h[:])
}

func TestReadWriteLockFile(t *testing.T) {
	dir := t.TempDir()

	original := &VendorLock{
		Deps: map[string]VendorDep{
			"htmx": {
				Version: "2.0.4",
				URL:     "https://unpkg.com/htmx.org@2.0.4/dist/htmx.min.js",
				Out:     "static/js/htmx.min.js",
				SHA256:  "abc123",
			},
		},
	}

	require.NoError(t, writeLockFile(dir, original))

	// File should exist.
	_, err := os.Stat(filepath.Join(dir, lockFileName))
	require.NoError(t, err)

	// Round-trip.
	loaded, err := readLockFile(dir)
	require.NoError(t, err)
	assert.Equal(t, original.Deps, loaded.Deps)
}

func TestReadLockFile_missing(t *testing.T) {
	dir := t.TempDir()

	lock, err := readLockFile(dir)
	require.NoError(t, err)
	assert.NotNil(t, lock.Deps)
	assert.Empty(t, lock.Deps)
}

func TestResolveURL(t *testing.T) {
	tests := []struct {
		tmpl    string
		version string
		want    string
	}{
		{
			tmpl:    "https://unpkg.com/htmx.org@{{.Version}}/dist/htmx.min.js",
			version: "2.0.4",
			want:    "https://unpkg.com/htmx.org@2.0.4/dist/htmx.min.js",
		},
		{
			tmpl:    "https://cdn.example.com/lib-{{.Version}}.js",
			version: "1.0.0",
			want:    "https://cdn.example.com/lib-1.0.0.js",
		},
		{
			tmpl:    "https://example.com/no-placeholder.js",
			version: "1.0.0",
			want:    "https://example.com/no-placeholder.js",
		},
	}

	for _, tt := range tests {
		got := resolveURL(tt.tmpl, tt.version)
		assert.Equal(t, tt.want, got)
	}
}

func TestParseNameVersion(t *testing.T) {
	name, ver := parseNameVersion("alpine@3.14.9")
	assert.Equal(t, "alpine", name)
	assert.Equal(t, "3.14.9", ver)

	name, ver = parseNameVersion("htmx")
	assert.Equal(t, "htmx", name)
	assert.Equal(t, "", ver)
}

func TestDownloadAndChecksum(t *testing.T) {
	content := "console.log('hello');"
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		fmt.Fprint(w, content)
	}))
	defer ts.Close()

	dir := t.TempDir()
	dest := filepath.Join(dir, "test.js")

	hash, err := downloadAndChecksum(ts.URL+"/test.js", dest)
	require.NoError(t, err)

	// Verify file was written.
	data, err := os.ReadFile(dest)
	require.NoError(t, err)
	assert.Equal(t, content, string(data))

	// Verify SHA256.
	assert.Equal(t, sha256hex(content), hash)
}

func TestDownloadAndChecksum_httpError(t *testing.T) {
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusNotFound)
	}))
	defer ts.Close()

	dir := t.TempDir()
	dest := filepath.Join(dir, "test.js")

	_, err := downloadAndChecksum(ts.URL+"/missing.js", dest)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "HTTP 404")
}

// newTestServer returns an httptest server that serves known JS content
// for the three default dependencies.
func newTestServer() *httptest.Server {
	mux := http.NewServeMux()
	mux.HandleFunc("/htmx.min.js", func(w http.ResponseWriter, r *http.Request) {
		fmt.Fprint(w, "/* htmx */")
	})
	mux.HandleFunc("/alpine.min.js", func(w http.ResponseWriter, r *http.Request) {
		fmt.Fprint(w, "/* alpine */")
	})
	mux.HandleFunc("/idiomorph.min.js", func(w http.ResponseWriter, r *http.Request) {
		fmt.Fprint(w, "/* idiomorph */")
	})
	mux.HandleFunc("/custom.js", func(w http.ResponseWriter, r *http.Request) {
		fmt.Fprint(w, "/* custom */")
	})
	return httptest.NewServer(mux)
}

// withTestRegistry temporarily replaces the default registry with one pointing
// at the test server, and restores it when the test completes.
func withTestRegistry(t *testing.T, baseURL string) {
	t.Helper()
	original := make(map[string]VendorDep, len(defaultRegistry))
	for k, v := range defaultRegistry {
		original[k] = v
	}

	defaultRegistry["htmx"] = VendorDep{
		Version: "2.0.4",
		URL:     baseURL + "/htmx.min.js",
		Out:     "static/js/htmx.min.js",
	}
	defaultRegistry["alpine"] = VendorDep{
		Version: "3.14.9",
		URL:     baseURL + "/alpine.min.js",
		Out:     "static/js/alpine.min.js",
	}
	defaultRegistry["idiomorph"] = VendorDep{
		Version: "0.3.0",
		URL:     baseURL + "/idiomorph.min.js",
		Out:     "static/js/idiomorph.min.js",
	}

	t.Cleanup(func() {
		for k, v := range original {
			defaultRegistry[k] = v
		}
	})
}

func TestVendorAll(t *testing.T) {
	ts := newTestServer()
	defer ts.Close()
	withTestRegistry(t, ts.URL)

	dir := t.TempDir()
	require.NoError(t, VendorAll(dir, false))

	// All 3 files should exist.
	assertFileExists(t, dir, "static/js/htmx.min.js")
	assertFileExists(t, dir, "static/js/alpine.min.js")
	assertFileExists(t, dir, "static/js/idiomorph.min.js")

	// Lock file should exist with 3 entries.
	lock, err := readLockFile(dir)
	require.NoError(t, err)
	assert.Len(t, lock.Deps, 3)

	// Verify checksums are recorded.
	assert.Equal(t, sha256hex("/* htmx */"), lock.Deps["htmx"].SHA256)
	assert.Equal(t, sha256hex("/* alpine */"), lock.Deps["alpine"].SHA256)
	assert.Equal(t, sha256hex("/* idiomorph */"), lock.Deps["idiomorph"].SHA256)
}

func TestVendorOne(t *testing.T) {
	ts := newTestServer()
	defer ts.Close()
	withTestRegistry(t, ts.URL)

	dir := t.TempDir()
	require.NoError(t, VendorOne(dir, "htmx", false))

	assertFileExists(t, dir, "static/js/htmx.min.js")
	assertFileNotExists(t, dir, "static/js/alpine.min.js")

	lock, err := readLockFile(dir)
	require.NoError(t, err)
	assert.Len(t, lock.Deps, 1)
	assert.Equal(t, "2.0.4", lock.Deps["htmx"].Version)
}

func TestVendorOne_withVersion(t *testing.T) {
	ts := newTestServer()
	defer ts.Close()
	withTestRegistry(t, ts.URL)

	dir := t.TempDir()
	require.NoError(t, VendorOne(dir, "alpine@3.14.9", false))

	assertFileExists(t, dir, "static/js/alpine.min.js")

	lock, err := readLockFile(dir)
	require.NoError(t, err)
	assert.Equal(t, "3.14.9", lock.Deps["alpine"].Version)
}

func TestVendorOne_unknownDep(t *testing.T) {
	dir := t.TempDir()
	err := VendorOne(dir, "jquery", false)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "unknown dependency")
}

func TestVendorVerify_pass(t *testing.T) {
	ts := newTestServer()
	defer ts.Close()
	withTestRegistry(t, ts.URL)

	dir := t.TempDir()
	require.NoError(t, VendorAll(dir, false))
	require.NoError(t, VendorVerify(dir))
}

func TestVendorVerify_mismatch(t *testing.T) {
	ts := newTestServer()
	defer ts.Close()
	withTestRegistry(t, ts.URL)

	dir := t.TempDir()
	require.NoError(t, VendorAll(dir, false))

	// Corrupt one file.
	err := os.WriteFile(filepath.Join(dir, "static/js/htmx.min.js"), []byte("corrupted"), 0o644)
	require.NoError(t, err)

	err = VendorVerify(dir)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "htmx")
	assert.Contains(t, err.Error(), "checksum mismatch")
}

func TestVendorVerify_missingFile(t *testing.T) {
	ts := newTestServer()
	defer ts.Close()
	withTestRegistry(t, ts.URL)

	dir := t.TempDir()
	require.NoError(t, VendorAll(dir, false))

	// Delete one file.
	require.NoError(t, os.Remove(filepath.Join(dir, "static/js/htmx.min.js")))

	err := VendorVerify(dir)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "htmx")
}

func TestVendorVerify_noLockFile(t *testing.T) {
	dir := t.TempDir()
	err := VendorVerify(dir)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "no dependencies")
}

func TestVendorSkipsExisting(t *testing.T) {
	downloads := 0
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		downloads++
		fmt.Fprint(w, "/* htmx */")
	}))
	defer ts.Close()

	original := defaultRegistry["htmx"]
	defaultRegistry["htmx"] = VendorDep{
		Version: "2.0.4",
		URL:     ts.URL + "/htmx.min.js",
		Out:     "static/js/htmx.min.js",
	}
	t.Cleanup(func() { defaultRegistry["htmx"] = original })

	dir := t.TempDir()

	// First call should download.
	require.NoError(t, VendorOne(dir, "htmx", false))
	assert.Equal(t, 1, downloads)

	// Second call with same version should skip (no re-download).
	require.NoError(t, VendorOne(dir, "htmx", false))
	assert.Equal(t, 1, downloads, "expected no re-download when version and checksum match")
}

func TestVendorCustom(t *testing.T) {
	ts := newTestServer()
	defer ts.Close()

	dir := t.TempDir()
	url := ts.URL + "/custom.js"
	out := "static/js/custom.min.js"

	require.NoError(t, VendorCustom(dir, url, out))

	assertFileExists(t, dir, out)

	data, err := os.ReadFile(filepath.Join(dir, out))
	require.NoError(t, err)
	assert.Equal(t, "/* custom */", string(data))

	lock, err := readLockFile(dir)
	require.NoError(t, err)
	assert.Contains(t, lock.Deps, "custom.min")
	assert.Equal(t, sha256hex("/* custom */"), lock.Deps["custom.min"].SHA256)
}
