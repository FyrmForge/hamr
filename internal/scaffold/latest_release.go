package scaffold

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"time"
)

// DefaultLatestReleaseTimeout is the default network timeout for FetchLatestRelease.
const DefaultLatestReleaseTimeout = 3 * time.Second

// githubLatestReleaseURL is the REST endpoint for the latest hamr release.
// A var (not const) so tests can point it at an httptest server.
var githubLatestReleaseURL = "https://api.github.com/repos/FyrmForge/hamr/releases/latest"

type githubRelease struct {
	TagName string `json:"tag_name"`
}

// FetchLatestRelease queries GitHub for the most recent hamr release and
// returns the parsed version. The supplied context bounds the request; a
// timeout shorter than DefaultLatestReleaseTimeout is recommended for
// interactive commands so users are not left hanging on network issues.
func FetchLatestRelease(ctx context.Context) (Version, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, githubLatestReleaseURL, nil)
	if err != nil {
		return Version{}, fmt.Errorf("build request: %w", err)
	}
	req.Header.Set("Accept", "application/vnd.github+json")

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return Version{}, fmt.Errorf("fetch latest release: %w", err)
	}
	defer func() { _ = resp.Body.Close() }()

	if resp.StatusCode != http.StatusOK {
		return Version{}, fmt.Errorf("fetch latest release: unexpected status %d", resp.StatusCode)
	}

	var rel githubRelease
	if err := json.NewDecoder(resp.Body).Decode(&rel); err != nil {
		return Version{}, fmt.Errorf("decode latest release: %w", err)
	}

	tag := strings.TrimPrefix(rel.TagName, "v")
	v, err := ParseVersion(tag)
	if err != nil {
		return Version{}, fmt.Errorf("parse tag %q: %w", rel.TagName, err)
	}
	return v, nil
}
