package cmd

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/spf13/cobra"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func newLocaleGenTestCmd() *cobra.Command {
	cmd := &cobra.Command{}
	cmd.Flags().String("config", "hamr.toml", "path to hamr.toml config file")
	cmd.Flags().String("dir", "", "locale directory (overrides config)")
	cmd.Flags().String("out", "", "output file (overrides config)")
	return cmd
}

func TestKeyToMethodName(t *testing.T) {
	tests := []struct {
		key  string
		want string
	}{
		{"app.title", "AppTitle"},
		{"home.welcome", "HomeWelcome"},
		{"home.items", "HomeItems"},
		{"nav.about", "NavAbout"},
		{"some_key", "SomeKey"},
		{"deep.nested.key", "DeepNestedKey"},
		{"kebab-case.key", "KebabCaseKey"},
		{"2fa.title", "X2faTitle"},
		{"404.error", "X404Error"},
		{"3rd_party.service", "X3rdPartyService"},
	}
	for _, tt := range tests {
		got := keyToMethodName(tt.key)
		assert.Equal(t, tt.want, got, "keyToMethodName(%q)", tt.key)
	}
}

func TestFlattenForGen(t *testing.T) {
	data := map[string]any{
		"_meta": map[string]any{"direction": "ltr"},
		"app": map[string]any{
			"title": "My App",
		},
		"home": map[string]any{
			"welcome": "Hello {{.Name}}!",
			"items": map[string]any{
				"one":   "{{.Count}} item",
				"other": "{{.Count}} items",
			},
		},
	}

	keys, err := flattenForGen(data, "")
	require.NoError(t, err)

	keyMap := make(map[string]localeKeyInfo)
	for _, k := range keys {
		keyMap[k.key] = k
	}

	// Plain key.
	assert.Contains(t, keyMap, "app.title")
	assert.False(t, keyMap["app.title"].isPlural)
	assert.Empty(t, keyMap["app.title"].params)

	// Interpolated key (Count excluded from params for plural).
	assert.Contains(t, keyMap, "home.welcome")
	assert.False(t, keyMap["home.welcome"].isPlural)
	assert.Equal(t, []string{"Name"}, keyMap["home.welcome"].params)

	// Plural key.
	assert.Contains(t, keyMap, "home.items")
	assert.True(t, keyMap["home.items"].isPlural)
	// Count should be excluded from params.
	assert.Empty(t, keyMap["home.items"].params)

	// _meta should be skipped.
	assert.NotContains(t, keyMap, "_meta")
	assert.NotContains(t, keyMap, "_meta.direction")
}

func TestGenerateGoSource(t *testing.T) {
	keys := []localeKeyInfo{
		{key: "app.title"},
		{key: "home.welcome", params: []string{"Name"}},
		{key: "home.items", isPlural: true},
	}

	src := generateGoSource("locale", keys)

	assert.Contains(t, src, "package locale")
	assert.Contains(t, src, "func (t *T) AppTitle() string")
	assert.Contains(t, src, "func (t *T) HomeWelcome(name string) string")
	assert.Contains(t, src, "func (t *T) HomeItems(count int) string")
	assert.Contains(t, src, "//go:generate hamr locale gen")
	assert.Contains(t, src, "DO NOT EDIT")
}

func TestRunLocaleGen(t *testing.T) {
	dir := t.TempDir()

	// Create locale files.
	localesDir := filepath.Join(dir, "locales")
	require.NoError(t, os.MkdirAll(localesDir, 0o755))

	en := `{
		"app": {"title": "My App"},
		"home": {
			"welcome": "Hello {{.Name}}!",
			"items": {"one": "{{.Count}} item", "other": "{{.Count}} items"}
		}
	}`
	require.NoError(t, os.WriteFile(filepath.Join(localesDir, "en.json"), []byte(en), 0o644))

	fr := `{
		"app": {"title": "Mon App"},
		"home": {
			"welcome": "Bonjour {{.Name}} !",
			"items": {"one": "{{.Count}} objet", "other": "{{.Count}} objets"}
		}
	}`
	require.NoError(t, os.WriteFile(filepath.Join(localesDir, "fr.json"), []byte(fr), 0o644))

	// Create hamr.toml.
	outPath := filepath.Join(dir, "internal/locale/locale.go")
	tomlContent := `[locale]
default = "en"
dir = "` + localesDir + `"
output = "` + outPath + `"
package = "locale"
`
	tomlPath := filepath.Join(dir, "hamr.toml")
	require.NoError(t, os.WriteFile(tomlPath, []byte(tomlContent), 0o644))

	// Run the command directly.
	cmd := newLocaleGenTestCmd()
	cmd.Flags().Set("config", tomlPath) //nolint:errcheck
	err := runLocaleGen(cmd, nil)
	require.NoError(t, err)

	// Verify output file.
	genData, err := os.ReadFile(outPath)
	require.NoError(t, err)

	src := string(genData)
	assert.Contains(t, src, "package locale")
	assert.Contains(t, src, "func (t *T) AppTitle() string")
	assert.Contains(t, src, "func (t *T) HomeWelcome(name string) string")
	assert.Contains(t, src, "func (t *T) HomeItems(count int) string")
}

func TestRunLocaleGenMissingExplicitConfig(t *testing.T) {
	cmd := newLocaleGenTestCmd()
	missingPath := filepath.Join(t.TempDir(), "missing.toml")
	cmd.Flags().Set("config", missingPath) //nolint:errcheck

	err := runLocaleGen(cmd, nil)
	require.Error(t, err)
	assert.ErrorContains(t, err, "reading config "+missingPath)
}

func TestExtractInterpolationParams(t *testing.T) {
	// Count is excluded.
	params := extractInterpolationParams("{{.Count}} items by {{.Author}}")
	assert.Equal(t, []string{"Author"}, params)

	// No params.
	params = extractInterpolationParams("Hello world")
	assert.Empty(t, params)

	// Multiple unique params.
	params = extractInterpolationParams("{{.First}} and {{.Last}} and {{.First}}")
	assert.Equal(t, []string{"First", "Last"}, params)
}

func TestGenerateGoSourcePluralWithParams(t *testing.T) {
	keys := []localeKeyInfo{
		{key: "inbox.messages", isPlural: true, params: []string{"User"}},
	}

	src := generateGoSource("locale", keys)
	assert.Contains(t, src, "func (t *T) InboxMessages(count int, user string) string")

	// Verify it contains the map construction.
	assert.True(t, strings.Contains(src, `"User": user`))
}
