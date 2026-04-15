package fingerprint

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestFingerprint(t *testing.T) {
	src := t.TempDir()
	dist := filepath.Join(t.TempDir(), "dist")

	// Create test files.
	writeFile(t, filepath.Join(src, "css", "output.css"), "body{color:red}")
	writeFile(t, filepath.Join(src, "js", "main.js"), "console.log('hi')")
	writeFile(t, filepath.Join(src, "img", "logo.png"), "fake-png-data")

	m, err := Fingerprint(src, dist)
	require.NoError(t, err)

	assert.Len(t, m.Files, 3)
	assert.Contains(t, m.Files, "css/output.css")
	assert.Contains(t, m.Files, "js/main.js")
	assert.Contains(t, m.Files, "img/logo.png")

	// Fingerprinted names should have the .HASH. pattern.
	for orig, fp := range m.Files {
		assert.True(t, fingerprintPattern.MatchString(fp), "fingerprinted path %q should contain hash", fp)
		// Extension should be preserved.
		assert.Equal(t, filepath.Ext(orig), filepath.Ext(fp))
		// Fingerprinted file should exist in dist.
		_, err := os.Stat(filepath.Join(dist, filepath.FromSlash(fp)))
		assert.NoError(t, err, "fingerprinted file %q should exist in dist", fp)
	}

	// Originals in src should be untouched.
	for orig := range m.Files {
		_, err := os.Stat(filepath.Join(src, filepath.FromSlash(orig)))
		assert.NoError(t, err, "original file %q should still exist in src", orig)
	}

	// Sentinel should exist in dist.
	_, err = os.Stat(filepath.Join(dist, sentinel))
	assert.NoError(t, err, "sentinel file should exist in dist")
}

func TestFingerprint_writesToDistNotSrc(t *testing.T) {
	src := t.TempDir()
	dist := filepath.Join(t.TempDir(), "dist")

	writeFile(t, filepath.Join(src, "app.css"), "body{}")

	m, err := Fingerprint(src, dist)
	require.NoError(t, err)

	fp := m.Files["app.css"]

	// Fingerprinted file should be in dist only.
	_, err = os.Stat(filepath.Join(dist, fp))
	assert.NoError(t, err)
	_, err = os.Stat(filepath.Join(src, fp))
	assert.True(t, os.IsNotExist(err), "fingerprinted file should NOT be in src")
}

func TestFingerprint_fingerprintsManifestJSON(t *testing.T) {
	src := t.TempDir()
	dist := filepath.Join(t.TempDir(), "dist")

	// A PWA manifest.json should be fingerprinted like any other file.
	writeFile(t, filepath.Join(src, "manifest.json"), `{"name":"myapp"}`)

	m, err := Fingerprint(src, dist)
	require.NoError(t, err)

	assert.Contains(t, m.Files, "manifest.json")
}

func TestClean(t *testing.T) {
	src := t.TempDir()
	dist := filepath.Join(t.TempDir(), "dist")

	// Create a valid dist with sentinel.
	writeFile(t, filepath.Join(src, "app.css"), "body{}")
	_, err := Fingerprint(src, dist)
	require.NoError(t, err)

	err = Clean(dist)
	require.NoError(t, err)

	// dist should be gone entirely.
	_, err = os.Stat(dist)
	assert.True(t, os.IsNotExist(err))
}

func TestClean_nonExistentDir(t *testing.T) {
	err := Clean(filepath.Join(t.TempDir(), "does-not-exist"))
	require.NoError(t, err)
}

func TestSkipGitkeep(t *testing.T) {
	src := t.TempDir()
	dist := filepath.Join(t.TempDir(), "dist")

	writeFile(t, filepath.Join(src, ".gitkeep"), "")
	writeFile(t, filepath.Join(src, "app.css"), "body{}")

	m, err := Fingerprint(src, dist)
	require.NoError(t, err)

	assert.Len(t, m.Files, 1)
	assert.Contains(t, m.Files, "app.css")
}

func TestReFingerprintWipesOldDist(t *testing.T) {
	src := t.TempDir()
	dist := filepath.Join(t.TempDir(), "dist")

	writeFile(t, filepath.Join(src, "app.css"), "v1")

	// First fingerprint.
	m1, err := Fingerprint(src, dist)
	require.NoError(t, err)
	fp1 := m1.Files["app.css"]

	// Change file content.
	writeFile(t, filepath.Join(src, "app.css"), "v2")

	// Re-fingerprint — should wipe dist and create new files.
	m2, err := Fingerprint(src, dist)
	require.NoError(t, err)
	fp2 := m2.Files["app.css"]

	assert.NotEqual(t, fp1, fp2, "hash should change with content")

	// Old fingerprinted file should be gone from dist.
	_, err = os.Stat(filepath.Join(dist, filepath.FromSlash(fp1)))
	assert.True(t, os.IsNotExist(err), "old fingerprinted file should be removed")

	// New fingerprinted file should exist in dist.
	_, err = os.Stat(filepath.Join(dist, filepath.FromSlash(fp2)))
	assert.NoError(t, err, "new fingerprinted file should exist")
}

func TestWriteGoManifest(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "staticmanifest.go")

	m := &Manifest{
		Files: map[string]string{
			"css/output.css": "css/output.a1b2c3d4e5f6.css",
			"js/main.js":     "js/main.7f8e9a0b1c2d.js",
		},
	}

	err := m.WriteGoManifest(path, "components")
	require.NoError(t, err)

	data, err := os.ReadFile(path)
	require.NoError(t, err)
	content := string(data)

	assert.Contains(t, content, "package components")
	assert.Contains(t, content, "DO NOT EDIT")
	assert.Contains(t, content, `"css/output.css": "css/output.a1b2c3d4e5f6.css"`)
	assert.Contains(t, content, `"js/main.js": "js/main.7f8e9a0b1c2d.js"`)
	assert.Contains(t, content, "var StaticManifest = map[string]string{")
}

func TestWriteGoManifest_empty(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "staticmanifest.go")

	m := &Manifest{Files: nil}

	err := m.WriteGoManifest(path, "components")
	require.NoError(t, err)

	data, err := os.ReadFile(path)
	require.NoError(t, err)
	content := string(data)

	assert.Contains(t, content, "package components")
	assert.Contains(t, content, "var StaticManifest map[string]string")
	assert.NotContains(t, content, "= map[string]string{")
}

func TestIsFingerprinted(t *testing.T) {
	assert.True(t, IsFingerprinted("/static/css/output.a1b2c3d4e5f6.css"))
	assert.True(t, IsFingerprinted("/static/js/main.789012345678.js"))
	assert.False(t, IsFingerprinted("/static/css/output.css"))
	assert.False(t, IsFingerprinted("/static/js/main.js"))
	assert.False(t, IsFingerprinted("/dashboard"))
}

func TestValidateDistDir_rejectsDot(t *testing.T) {
	_, err := Fingerprint(t.TempDir(), ".")
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "not a safe path")
}

func TestValidateDistDir_rejectsDotDot(t *testing.T) {
	_, err := Fingerprint(t.TempDir(), "..")
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "not a safe path")
}

func TestValidateDistDir_rejectsSameAsSrc(t *testing.T) {
	src := t.TempDir()
	_, err := Fingerprint(src, src)
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "must not be the same as source")
}

func TestValidateDistDir_rejectsNonOwnedDir(t *testing.T) {
	src := t.TempDir()
	dist := filepath.Join(t.TempDir(), "dist")

	// Create dist with files but no sentinel.
	writeFile(t, filepath.Join(dist, "important.txt"), "don't delete me")

	_, err := Fingerprint(src, dist)
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "not created by hamr gen static")
}

func TestValidateDistDir_allowsEmptyExistingDir(t *testing.T) {
	src := t.TempDir()
	dist := filepath.Join(t.TempDir(), "dist")
	require.NoError(t, os.MkdirAll(dist, 0o755))

	writeFile(t, filepath.Join(src, "app.css"), "body{}")

	_, err := Fingerprint(src, dist)
	assert.NoError(t, err)
}

// writeFile creates parent dirs and writes content.
func writeFile(t *testing.T, path, content string) {
	t.Helper()
	require.NoError(t, os.MkdirAll(filepath.Dir(path), 0o755))
	require.NoError(t, os.WriteFile(path, []byte(content), 0o644))
}
