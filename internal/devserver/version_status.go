package devserver

// VersionStatus indicates the CLI-vs-project version state.
type VersionStatus int

const (
	VersionOK       VersionStatus = iota // versions match or no project version
	VersionDev                           // CLI is a dev build
	VersionMismatch                      // CLI major.minor differs from project
	VersionUpdate                        // newer version available on GitHub
)
