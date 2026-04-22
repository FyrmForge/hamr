package devserver

import (
	"context"

	"github.com/FyrmForge/hamr/internal/scaffold"
)

// CheckLatestVersion checks GitHub for the latest hamr release and calls onResult
// with the latest version string if it is newer than current. The check runs in a
// goroutine and is best-effort — network errors are silently ignored.
func CheckLatestVersion(ctx context.Context, currentVersion string, onResult func(latest string)) {
	current, err := scaffold.ParseVersion(currentVersion)
	if err != nil {
		return
	}

	go func() {
		checkCtx, cancel := context.WithTimeout(ctx, scaffold.DefaultLatestReleaseTimeout)
		defer cancel()

		latest, err := scaffold.FetchLatestRelease(checkCtx)
		if err != nil {
			return
		}

		if current.Less(latest) {
			onResult(latest.String())
		}
	}()
}
