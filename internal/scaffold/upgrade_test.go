package scaffold

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

var testChanges = []Change{
	{
		Category:              CategoryStructural,
		Title:                 "Group-based route registration",
		Since:                 "0.4.0",
		Summary:               "Routes moved from flat to group-based pattern",
		AffectedScaffoldFiles: []string{"internal/web/server.go"},
	},
	{
		Category:              CategoryPackageUpdate,
		Title:                 "Validation package v2",
		Since:                 "0.4.2",
		Summary:               "Validate API changed",
		AffectedScaffoldFiles: []string{"internal/web/handler/auth/handler.go"},
	},
	{
		Category:              CategoryNewPackage,
		Title:                 "Rate limiting middleware",
		Since:                 "0.5.0",
		Summary:               "Built-in rate limiting added",
		AffectedScaffoldFiles: []string{"internal/web/server.go"},
	},
	{
		Category:              CategoryPackageUpdate,
		Title:                 "Mailer improvements",
		Since:                 "0.4.5",
		Summary:               "Mailer gained template support",
		AffectedScaffoldFiles: []string{"internal/mailer/mailer.go"},
		RelevantOptions:       []string{"locale"},
	},
	{
		Category:              CategoryNewOption,
		Title:                 "Stripe webhook v2",
		Since:                 "0.5.0",
		Summary:               "New Stripe webhook handler pattern",
		AffectedScaffoldFiles: []string{"internal/api/handler/stripe/handler.go"},
		RelevantOptions:       []string{"stripe"},
	},
}

func TestBuildReport(t *testing.T) {
	meta := Metadata{
		Hamr:    HamrSection{Version: "0.3.2", ScaffoldedAt: "2026-03-11"},
		Options: Options{Database: "postgres", Auth: "session", CSS: "tailwind"},
	}

	t.Run("all changes since base version", func(t *testing.T) {
		report, err := BuildReport(meta, "0.5.0", testChanges, ReportFilters{})
		require.NoError(t, err)

		assert.Equal(t, "0.3.2", report.Project.BaseVersion)
		assert.Equal(t, "0.5.0", report.Project.CurrentVersion)
		assert.Equal(t, "2026-03-11", report.Project.ScaffoldedAt)
		assert.Len(t, report.Changes, 5)
	})

	t.Run("filter by category", func(t *testing.T) {
		report, err := BuildReport(meta, "0.5.0", testChanges, ReportFilters{
			Category: CategoryStructural,
		})
		require.NoError(t, err)

		assert.Len(t, report.Changes, 1)
		assert.Equal(t, "Group-based route registration", report.Changes[0].Title)
	})

	t.Run("relevant only", func(t *testing.T) {
		report, err := BuildReport(meta, "0.5.0", testChanges, ReportFilters{
			RelevantOnly: true,
		})
		require.NoError(t, err)

		// Mailer (locale) and Stripe are not relevant — 3 universal changes remain.
		assert.Len(t, report.Changes, 3)
		for _, c := range report.Changes {
			assert.True(t, c.Relevant)
		}
	})

	t.Run("irrelevant changes have reason", func(t *testing.T) {
		report, err := BuildReport(meta, "0.5.0", testChanges, ReportFilters{})
		require.NoError(t, err)

		for _, c := range report.Changes {
			if !c.Relevant {
				assert.NotEmpty(t, c.RelevanceReason)
			}
		}
	})

	t.Run("from version override", func(t *testing.T) {
		report, err := BuildReport(meta, "0.5.0", testChanges, ReportFilters{
			FromVersion: "0.4.1",
		})
		require.NoError(t, err)

		// Only changes since 0.4.1: validation (0.4.2), mailer (0.4.5), rate limit (0.5.0), stripe (0.5.0)
		assert.Len(t, report.Changes, 4)
	})

	t.Run("no changes when versions match", func(t *testing.T) {
		report, err := BuildReport(meta, "0.3.2", testChanges, ReportFilters{})
		require.NoError(t, err)
		assert.Empty(t, report.Changes)
	})

	t.Run("empty changes list", func(t *testing.T) {
		report, err := BuildReport(meta, "0.5.0", nil, ReportFilters{})
		require.NoError(t, err)
		assert.NotNil(t, report.Changes)
		assert.Empty(t, report.Changes)
	})

	t.Run("invalid base version", func(t *testing.T) {
		bad := Metadata{Hamr: HamrSection{Version: "bad"}}
		_, err := BuildReport(bad, "0.5.0", testChanges, ReportFilters{})
		require.Error(t, err)
	})

	t.Run("invalid current version", func(t *testing.T) {
		_, err := BuildReport(meta, "bad", testChanges, ReportFilters{})
		require.Error(t, err)
	})

	t.Run("changes beyond current version excluded", func(t *testing.T) {
		report, err := BuildReport(meta, "0.4.3", testChanges, ReportFilters{})
		require.NoError(t, err)

		// Only 0.4.0 and 0.4.2 should be included (not 0.4.5 or 0.5.0).
		assert.Len(t, report.Changes, 2)
	})
}

func TestUpdateVersion(t *testing.T) {
	t.Run("updates version line", func(t *testing.T) {
		content := `[hamr]
version = "0.3.2"
scaffolded_at = "2026-03-11"

[options]
database = "postgres"

[proxy]
listen = ":3000"
`
		path := writeTempTOML(t, content)
		require.NoError(t, UpdateVersion(path, "0.5.0"))

		updated, err := os.ReadFile(path)
		require.NoError(t, err)

		assert.Contains(t, string(updated), `version = "0.5.0"`)
		assert.Contains(t, string(updated), `scaffolded_at = "2026-03-11"`)
		assert.Contains(t, string(updated), `database = "postgres"`)
		assert.Contains(t, string(updated), `listen = ":3000"`)
	})

	t.Run("preserves comments", func(t *testing.T) {
		content := `[hamr]
version = "0.3.2"
# This is a comment
scaffolded_at = "2026-03-11"

[proxy]
listen = ":3000"
`
		path := writeTempTOML(t, content)
		require.NoError(t, UpdateVersion(path, "1.0.0"))

		updated, err := os.ReadFile(path)
		require.NoError(t, err)

		assert.Contains(t, string(updated), "# This is a comment")
		assert.Contains(t, string(updated), `version = "1.0.0"`)
	})

	t.Run("missing hamr section inserts it", func(t *testing.T) {
		content := `[proxy]
listen = ":3000"
`
		path := writeTempTOML(t, content)
		require.NoError(t, UpdateVersion(path, "1.0.0"))

		updated, err := os.ReadFile(path)
		require.NoError(t, err)

		assert.Contains(t, string(updated), `[hamr]`)
		assert.Contains(t, string(updated), `version = "1.0.0"`)
		assert.Contains(t, string(updated), `listen = ":3000"`)
	})

	t.Run("missing file", func(t *testing.T) {
		err := UpdateVersion(filepath.Join(t.TempDir(), "nonexistent.toml"), "1.0.0")
		require.Error(t, err)
	})
}
