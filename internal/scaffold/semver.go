package scaffold

import (
	"fmt"
	"strconv"
	"strings"
)

// Version represents a semantic version with major, minor, and patch components.
type Version struct {
	Major int
	Minor int
	Patch int
}

// ParseVersion parses a version string like "v0.3.2", "0.3.2", or "0.3.2-dev"
// into a Version. Pre-release suffixes ("-dev", "-rc.1") and SemVer build
// metadata ("+build.42") are both stripped.
func ParseVersion(s string) (Version, error) {
	s = strings.TrimPrefix(s, "v")
	// Strip SemVer build metadata (everything after first '+').
	if i := strings.IndexByte(s, '+'); i != -1 {
		s = s[:i]
	}
	// Strip pre-release suffix (everything after first hyphen).
	if i := strings.IndexByte(s, '-'); i != -1 {
		s = s[:i]
	}
	parts := strings.Split(s, ".")
	if len(parts) != 3 {
		return Version{}, fmt.Errorf("invalid version %q: expected major.minor.patch", s)
	}

	major, err := strconv.Atoi(parts[0])
	if err != nil {
		return Version{}, fmt.Errorf("invalid major version %q: %w", parts[0], err)
	}
	minor, err := strconv.Atoi(parts[1])
	if err != nil {
		return Version{}, fmt.Errorf("invalid minor version %q: %w", parts[1], err)
	}
	patch, err := strconv.Atoi(parts[2])
	if err != nil {
		return Version{}, fmt.Errorf("invalid patch version %q: %w", parts[2], err)
	}

	if major < 0 || minor < 0 || patch < 0 {
		return Version{}, fmt.Errorf("invalid version %q: components must not be negative", s)
	}

	return Version{Major: major, Minor: minor, Patch: patch}, nil
}

// Less returns true if v is strictly less than other.
func (v Version) Less(other Version) bool {
	if v.Major != other.Major {
		return v.Major < other.Major
	}
	if v.Minor != other.Minor {
		return v.Minor < other.Minor
	}
	return v.Patch < other.Patch
}

// String returns the version as "major.minor.patch".
func (v Version) String() string {
	return fmt.Sprintf("%d.%d.%d", v.Major, v.Minor, v.Patch)
}
