package cmd

import (
	"testing"

	"github.com/FyrmForge/hamr/internal/cli/generator"
	"github.com/stretchr/testify/assert"
)

func TestApplyWizardResult_defaults(t *testing.T) {
	res := &wizardResult{
		Owner:          "darthvader",
		CSS:            "tailwind",
		Database:       "postgres",
		StorageBackend: "local",
		WebSocket:      "yes",
	}

	cfg := &generator.ProjectConfig{
		Name:            "myapp",
		GoVersion:       generator.DetectGoVersion(),
		IncludeAuth:     true,
		AuthWithTables:  true,
		IncludeSessions: true,
	}
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
	assert.False(t, cfg.IncludeE2E)
}

func TestApplyWizardResult_s3Storage(t *testing.T) {
	res := &wizardResult{
		Owner:          "user",
		CSS:            "plain",
		Database:       "postgres",
		StorageBackend: "s3",
	}

	cfg := &generator.ProjectConfig{
		Name:            "app",
		GoVersion:       generator.DetectGoVersion(),
		IncludeAuth:     true,
		AuthWithTables:  true,
		IncludeSessions: true,
	}
	applyWizardResult(newCmd, "app", res, cfg)

	assert.True(t, cfg.IncludeStorage)
	assert.Equal(t, "s3", cfg.StorageBackend)
}

func TestApplyWizardResult_noStorage(t *testing.T) {
	res := &wizardResult{
		Owner:          "user",
		CSS:            "plain",
		Database:       "postgres",
		StorageBackend: "none",
	}

	cfg := &generator.ProjectConfig{
		Name:            "app",
		GoVersion:       generator.DetectGoVersion(),
		IncludeAuth:     true,
		AuthWithTables:  true,
		IncludeSessions: true,
	}
	applyWizardResult(newCmd, "app", res, cfg)

	assert.False(t, cfg.IncludeStorage)
	assert.Equal(t, "", cfg.StorageBackend)
}

func TestApplyWizardResult_staticS3(t *testing.T) {
	res := &wizardResult{
		Owner:          "user",
		CSS:            "plain",
		Database:       "postgres",
		StorageBackend: "s3",
		StaticS3:       "yes",
	}

	cfg := &generator.ProjectConfig{
		Name:      "app",
		GoVersion: "1.25.0",
	}
	applyWizardResult(newCmd, "app", res, cfg)

	assert.True(t, cfg.IncludeStorage)
	assert.Equal(t, "s3", cfg.StorageBackend)
	assert.True(t, cfg.StaticS3)
}

func TestApplyWizardResult_staticS3_no(t *testing.T) {
	res := &wizardResult{
		Owner:          "user",
		CSS:            "plain",
		Database:       "postgres",
		StorageBackend: "s3",
		StaticS3:       "no",
	}

	cfg := &generator.ProjectConfig{
		Name:      "app",
		GoVersion: "1.25.0",
	}
	applyWizardResult(newCmd, "app", res, cfg)

	assert.True(t, cfg.IncludeStorage)
	assert.Equal(t, "s3", cfg.StorageBackend)
	assert.False(t, cfg.StaticS3)
}

func TestApplyWizardResult_staticS3_nonS3Storage(t *testing.T) {
	res := &wizardResult{
		Owner:          "user",
		CSS:            "plain",
		Database:       "postgres",
		StorageBackend: "local",
		StaticS3:       "yes",
	}

	cfg := &generator.ProjectConfig{
		Name:      "app",
		GoVersion: "1.25.0",
	}
	applyWizardResult(newCmd, "app", res, cfg)

	assert.False(t, cfg.StaticS3, "StaticS3 should only be set for s3 storage backend")
}

func TestWizardResult_locationDefault(t *testing.T) {
	res := &wizardResult{
		Location: "subfolder",
	}
	assert.Equal(t, "subfolder", res.Location)
}
