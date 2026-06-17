package generator

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
)

// writeFile is a small helper for building a temp project tree.
func writeFile(t *testing.T, dir, rel, content string) string {
	t.Helper()
	path := filepath.Join(dir, rel)
	require.NoError(t, os.MkdirAll(filepath.Dir(path), 0o755))
	require.NoError(t, os.WriteFile(path, []byte(content), 0o644))
	return path
}

func TestRenameModule_SkipsExcludedDirs(t *testing.T) {
	dir := t.TempDir()
	const oldMod = "github.com/old/proj"
	const newMod = "github.com/new/proj"

	writeFile(t, dir, "go.mod", "module "+oldMod+"\n\ngo 1.25\n")

	// Regular source that imports the old module — must be rewritten.
	src := writeFile(t, dir, "main.go",
		"package main\n\nimport _ \""+oldMod+"/pkg/foo\"\n\nfunc main() {}\n")

	// Vendored third-party copy importing the old module — must NOT be touched.
	vendored := writeFile(t, dir, "vendor/github.com/x/y/y.go",
		"package y\n\nimport _ \""+oldMod+"/pkg/foo\"\n")

	// Intentionally malformed fixture under testdata — would abort the walk on
	// parse if not skipped.
	writeFile(t, dir, "testdata/broken.go", "package this is not valid go {{{")

	oldModule, filesUpdated, err := RenameModule(dir, newMod, false)
	require.NoError(t, err)
	require.Equal(t, oldMod, oldModule)
	require.Equal(t, 1, filesUpdated, "only the regular source file should be rewritten")

	srcData, err := os.ReadFile(src)
	require.NoError(t, err)
	require.Contains(t, string(srcData), newMod, "regular import should be rewritten")
	require.NotContains(t, string(srcData), oldMod)

	vendoredData, err := os.ReadFile(vendored)
	require.NoError(t, err)
	require.Contains(t, string(vendoredData), oldMod, "vendored import must be left untouched")

	goModData, err := os.ReadFile(filepath.Join(dir, "go.mod"))
	require.NoError(t, err)
	require.True(t, strings.HasPrefix(string(goModData), "module "+newMod),
		"go.mod module directive should be updated")
}

func TestRenameModule_DoesNotWriteThroughSymlink(t *testing.T) {
	dir := t.TempDir()
	const oldMod = "github.com/old/proj"
	const newMod = "github.com/new/proj"

	writeFile(t, dir, "go.mod", "module "+oldMod+"\n\ngo 1.25\n")

	// A sensitive file outside the project tree.
	outside := t.TempDir()
	target := filepath.Join(outside, "secret")
	const secret = "do-not-touch\n"
	require.NoError(t, os.WriteFile(target, []byte(secret), 0o644))

	// A .go entry inside the project that is actually a symlink to the target.
	require.NoError(t, os.Symlink(target, filepath.Join(dir, "evil.go")))

	_, _, err := RenameModule(dir, newMod, false)
	require.NoError(t, err)

	got, err := os.ReadFile(target)
	require.NoError(t, err)
	require.Equal(t, secret, string(got), "symlink target must not be written through")
}

// TestRewriteTemplImports_OnlyRewritesImportLines guards the scoping fix: the
// module path must be rewritten in import statements but left untouched where it
// legitimately appears in markup (string literals, attributes).
func TestRewriteTemplImports_OnlyRewritesImportLines(t *testing.T) {
	dir := t.TempDir()
	const oldMod = "github.com/old/proj"
	const newMod = "github.com/new/proj"
	content := `package views

import (
	"github.com/old/proj/components"
)

templ Page() {
	<span>{ "github.com/old/proj" }</span>
}
`
	path := filepath.Join(dir, "page.templ")
	require.NoError(t, os.WriteFile(path, []byte(content), 0o644))

	changed, err := rewriteTemplImports(path, oldMod, newMod, false)
	require.NoError(t, err)
	require.True(t, changed)

	got, err := os.ReadFile(path)
	require.NoError(t, err)
	s := string(got)
	require.Contains(t, s, `"github.com/new/proj/components"`, "import must be rewritten")
	require.Contains(t, s, `{ "github.com/old/proj" }`, "markup string literal must NOT be rewritten")
}
