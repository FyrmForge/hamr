package scaffold

import (
	"fmt"
	"log/slog"
	"os"

	"github.com/BurntSushi/toml"
)

// DefaultAIDir is the default directory for AI command artifacts.
const DefaultAIDir = ".hamr/ai"

// AIConfig holds the [ai] section of hamr.toml.
type AIConfig struct {
	Dir string `toml:"dir"`
}

// Metadata represents the [hamr], [options], [ai], and [locale] sections of hamr.toml.
type Metadata struct {
	Hamr    HamrSection   `toml:"hamr"`
	Options Options       `toml:"options"`
	AI      AIConfig      `toml:"ai"`
	Locale  LocaleSection `toml:"locale"`
}

// LocaleSection holds the [locale] table fields the CLI reads.
type LocaleSection struct {
	Default string `toml:"default"`
}

// HamrSection holds the HAMR scaffold tracking fields.
type HamrSection struct {
	Version     string `toml:"version"`
	ScaffoldedAt string `toml:"scaffolded_at"`
}

// Options records the scaffold choices made when the project was generated.
type Options struct {
	Database    string `toml:"database"`
	DBConnector string `toml:"db_connector"`
	Auth        string `toml:"auth"`
	CSS         string `toml:"css"`
	WebSockets  bool   `toml:"websockets"`
	E2E         bool   `toml:"e2e"`
	Stripe      bool   `toml:"stripe"`
	Locale      bool   `toml:"locale"`
	Storage     string `toml:"storage"`
	Alpine      bool   `toml:"alpine"`
}

// HasHamrSection reports whether the loaded metadata contained a [hamr] section.
// When false, the project was scaffolded before metadata tracking was added.
func (m Metadata) HasHamrSection() bool {
	return m.Hamr.Version != ""
}

// AIDir returns the configured AI directory, falling back to DefaultAIDir.
func (m Metadata) AIDir() string {
	if m.AI.Dir != "" {
		return m.AI.Dir
	}
	return DefaultAIDir
}

// ResolveAIDir loads metadata from tomlPath and returns the AI directory.
// Falls back to DefaultAIDir on any error, logging a warning when the file
// exists but cannot be parsed.
func ResolveAIDir(tomlPath string) string {
	meta, err := LoadMetadata(tomlPath)
	if err != nil {
		if _, statErr := os.Stat(tomlPath); statErr == nil {
			slog.Warn("failed to parse hamr.toml, using default AI directory", "path", tomlPath, "error", err)
		}
		return DefaultAIDir
	}
	return meta.AIDir()
}

// LoadMetadata reads hamr.toml and decodes the [hamr] and [options] sections.
// Returns zero Metadata (with empty version) when [hamr] is absent.
func LoadMetadata(path string) (Metadata, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return Metadata{}, fmt.Errorf("read metadata: %w", err)
	}

	var meta Metadata
	_, err = toml.Decode(string(data), &meta)
	if err != nil {
		return Metadata{}, fmt.Errorf("parse metadata: %w", err)
	}

	return meta, nil
}
