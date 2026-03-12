package scaffold

import (
	"fmt"
	"os"

	"github.com/BurntSushi/toml"
)

// Metadata represents the [hamr] and [options] sections of hamr.toml.
type Metadata struct {
	Hamr    HamrSection `toml:"hamr"`
	Options Options     `toml:"options"`
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
}

// HasHamrSection reports whether the loaded metadata contained a [hamr] section.
// When false, the project was scaffolded before metadata tracking was added.
func (m Metadata) HasHamrSection() bool {
	return m.Hamr.Version != ""
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
