package generator

import (
	"fmt"
	"regexp"
	"strings"
)

var projectNamePattern = regexp.MustCompile(`^[a-zA-Z][a-zA-Z0-9._-]*$`)

const ProjectNameFormatMessage = "must start with a letter and contain only letters, digits, periods, hyphens, or underscores"

// IsValidProjectName reports whether name is a valid HAMR project name.
func IsValidProjectName(name string) bool {
	return projectNamePattern.MatchString(name)
}

// ValidateProjectName validates a HAMR project name.
func ValidateProjectName(name string) error {
	if name == "" {
		return fmt.Errorf("project name is required")
	}
	if !IsValidProjectName(name) {
		return fmt.Errorf(ProjectNameFormatMessage)
	}
	return nil
}

// ProjectSlug returns a lowercase, hyphenated identifier safe for generated
// infrastructure defaults such as Docker Compose, database names, and buckets.
func ProjectSlug(name string) string {
	var b strings.Builder
	prevHyphen := false

	for _, r := range strings.ToLower(name) {
		switch {
		case r >= 'a' && r <= 'z', r >= '0' && r <= '9':
			b.WriteRune(r)
			prevHyphen = false
		case r == '.', r == '-', r == '_':
			if b.Len() > 0 && !prevHyphen {
				b.WriteByte('-')
				prevHyphen = true
			}
		}
	}

	return strings.Trim(b.String(), "-")
}
