package cmd

import (
	"testing"

	"github.com/FyrmForge/hamr/internal/cli/generator"
	"github.com/stretchr/testify/assert"
)

func TestApplyWizardResult_fullAuth(t *testing.T) {
	res := &wizardResult{
		Owner:          "darthvader",
		CSS:            "tailwind",
		Database:       "postgres",
		Auth:           "full",
		Sessions:       "yes",
		StorageBackend: "local",
		WebSocket:      "yes",
	}

	cfg := &generator.ProjectConfig{Name: "myapp", GoVersion: "1.25.0"}
	applyWizardResult(newCmd, "myapp", res, cfg)

	assert.Equal(t, "github.com/darthvader/myapp", cfg.Module)
	assert.Equal(t, "tailwind", cfg.CSS)
	assert.Equal(t, "postgres", cfg.Database)
	assert.True(t, cfg.IncludeAuth)
	assert.True(t, cfg.AuthWithTables)
	assert.True(t, cfg.IncludeSessions)
	assert.True(t, cfg.IncludeStorage)
	assert.Equal(t, "local", cfg.StorageBackend)
	assert.True(t, cfg.IncludeWS)
	assert.False(t, cfg.IncludeNotify)
	assert.False(t, cfg.IncludeE2E)
}

func TestApplyWizardResult_emptyAuth(t *testing.T) {
	res := &wizardResult{
		Owner:    "org",
		CSS:      "plain",
		Database: "postgres",
		Auth:     "empty",
		Sessions: "yes",
	}

	cfg := &generator.ProjectConfig{Name: "app", GoVersion: "1.25.0"}
	applyWizardResult(newCmd, "app", res, cfg)

	assert.True(t, cfg.IncludeAuth)
	assert.False(t, cfg.AuthWithTables)
}

func TestApplyWizardResult_noAuth(t *testing.T) {
	res := &wizardResult{
		Owner:    "user",
		CSS:      "plain",
		Database: "postgres",
		Auth:     "none",
		Sessions: "no",
	}

	cfg := &generator.ProjectConfig{Name: "app", GoVersion: "1.25.0"}
	applyWizardResult(newCmd, "app", res, cfg)

	assert.False(t, cfg.IncludeAuth)
	assert.False(t, cfg.AuthWithTables)
	assert.False(t, cfg.IncludeSessions)
}

func TestApplyWizardResult_s3Storage(t *testing.T) {
	res := &wizardResult{
		Owner:           "user",
		CSS:             "plain",
		Database:        "postgres",
		Auth:            "none",
		StorageBackend:  "s3",
		S3StaticWatcher: "yes",
	}

	cfg := &generator.ProjectConfig{Name: "app", GoVersion: "1.25.0"}
	applyWizardResult(newCmd, "app", res, cfg)

	assert.True(t, cfg.IncludeStorage)
	assert.Equal(t, "s3", cfg.StorageBackend)
	assert.True(t, cfg.S3StaticWatcher)
}

func TestApplyWizardResult_noStorage(t *testing.T) {
	res := &wizardResult{
		Owner:          "user",
		CSS:            "plain",
		Database:       "postgres",
		Auth:           "none",
		StorageBackend: "none",
	}

	cfg := &generator.ProjectConfig{Name: "app", GoVersion: "1.25.0"}
	applyWizardResult(newCmd, "app", res, cfg)

	assert.False(t, cfg.IncludeStorage)
	assert.Equal(t, "", cfg.StorageBackend)
	assert.False(t, cfg.S3StaticWatcher)
}

func TestApplyFlags_defaults(t *testing.T) {
	cfg := &generator.ProjectConfig{Name: "proj", GoVersion: "1.25.0"}
	applyFlags(newCmd, "proj", cfg)

	assert.Equal(t, "github.com/user/proj", cfg.Module)
	assert.Equal(t, "plain", cfg.CSS)
	assert.Equal(t, "postgres", cfg.Database)
	assert.True(t, cfg.IncludeSessions)
	assert.False(t, cfg.IncludeStorage)
	assert.Equal(t, "", cfg.StorageBackend)
	assert.False(t, cfg.IncludeWS)
	assert.False(t, cfg.IncludeNotify)
	assert.False(t, cfg.IncludeE2E)
	assert.False(t, cfg.IncludeAuth)
}

func TestWizardResult_locationDefault(t *testing.T) {
	res := &wizardResult{
		Location: "subfolder",
	}
	assert.Equal(t, "subfolder", res.Location)
}
