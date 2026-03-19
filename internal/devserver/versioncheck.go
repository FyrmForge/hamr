package devserver

import (
	"context"
	"encoding/json"
	"net/http"
	"strings"
	"time"

	"github.com/FyrmForge/hamr/internal/scaffold"
)

const (
	githubReleasesURL  = "https://api.github.com/repos/FyrmForge/hamr/releases/latest"
	versionCheckTimeout = 3 * time.Second
)

// githubRelease is the minimal subset of the GitHub release API response.
type githubRelease struct {
	TagName string `json:"tag_name"`
}

// CheckLatestVersion checks GitHub for the latest hamr release and calls onResult
// with the latest version string if it is newer than current. The check runs in a
// goroutine and is best-effort — network errors are silently ignored.
func CheckLatestVersion(ctx context.Context, currentVersion string, onResult func(latest string)) {
	current, err := scaffold.ParseVersion(currentVersion)
	if err != nil {
		return
	}

	go func() {
		checkCtx, cancel := context.WithTimeout(ctx, versionCheckTimeout)
		defer cancel()

		req, err := http.NewRequestWithContext(checkCtx, http.MethodGet, githubReleasesURL, nil)
		if err != nil {
			return
		}
		req.Header.Set("Accept", "application/vnd.github+json")

		resp, err := http.DefaultClient.Do(req)
		if err != nil {
			return
		}
		defer func() { _ = resp.Body.Close() }()

		if resp.StatusCode != http.StatusOK {
			return
		}

		var rel githubRelease
		if err := json.NewDecoder(resp.Body).Decode(&rel); err != nil {
			return
		}

		tag := strings.TrimPrefix(rel.TagName, "v")
		latest, err := scaffold.ParseVersion(tag)
		if err != nil {
			return
		}

		if current.Less(latest) {
			onResult(latest.String())
		}
	}()
}
