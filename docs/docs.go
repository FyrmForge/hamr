// Package docs embeds the human-readable guide (docs/guide) so sites such as
// hamrstack.io can render the docs that match a pinned hamr version.
package docs

import "embed"

// Guide holds docs/guide: the chapters, cli.md, hamr-toml.md, the guide
// README.md (which defines the navigation) and pkg/*.md.
//
//go:embed guide
var Guide embed.FS
