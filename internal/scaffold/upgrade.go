package scaffold

import (
	"fmt"
	"os"
	"regexp"
	"strings"
)

// versionLineRe matches the version = "..." line under [hamr].
var versionLineRe = regexp.MustCompile(`(?m)^(\s*version\s*=\s*)"[^"]*"`)

// UpdateVersion reads hamr.toml, replaces the version value under [hamr], and writes back.
// Uses line-level replacement to preserve comments and formatting.
// The original file is restored if verification fails.
func UpdateVersion(path string, newVersion string) error {
	data, err := os.ReadFile(path)
	if err != nil {
		return fmt.Errorf("read file: %w", err)
	}

	content := string(data)

	var newContent string

	// Find the [hamr] section and replace the version line within it.
	hamrIdx := strings.Index(content, "[hamr]")
	if hamrIdx == -1 {
		// No [hamr] section — insert one at the top of the file.
		header := fmt.Sprintf("[hamr]\nversion = %q\n\n", newVersion)
		newContent = header + content
	} else {
		// Find the next section header after [hamr] to limit our search.
		afterHamr := content[hamrIdx:]
		nextSection := findNextSection(afterHamr)

		hamrSection := afterHamr
		if nextSection > 0 {
			hamrSection = afterHamr[:nextSection]
		}

		replaced := versionLineRe.ReplaceAllString(hamrSection, `${1}"`+newVersion+`"`)
		if replaced == hamrSection {
			return fmt.Errorf("could not find version line in [hamr] section")
		}

		if nextSection > 0 {
			newContent = content[:hamrIdx] + replaced + afterHamr[nextSection:]
		} else {
			newContent = content[:hamrIdx] + replaced
		}
	}

	if err := os.WriteFile(path, []byte(newContent), 0o644); err != nil {
		return err
	}

	// Verify the write produced valid TOML with the correct version.
	// Restore the original file on failure.
	updated, err := LoadMetadata(path)
	if err != nil {
		_ = os.WriteFile(path, data, 0o644)
		return fmt.Errorf("verify version update: file is no longer valid TOML: %w", err)
	}
	if updated.Hamr.Version != newVersion {
		_ = os.WriteFile(path, data, 0o644)
		return fmt.Errorf("verify version update: expected %q, got %q", newVersion, updated.Hamr.Version)
	}

	return nil
}

// findNextSection returns the offset of the next [section] header after the first line.
// Returns -1 if no next section is found.
func findNextSection(s string) int {
	// Skip the first line (the [hamr] header itself).
	firstNewline := strings.IndexByte(s, '\n')
	if firstNewline == -1 {
		return -1
	}

	rest := s[firstNewline+1:]
	lines := strings.Split(rest, "\n")
	offset := firstNewline + 1
	for _, line := range lines {
		trimmed := strings.TrimSpace(line)
		if strings.HasPrefix(trimmed, "[") && !strings.HasPrefix(trimmed, "[[") {
			return offset
		}
		offset += len(line) + 1 // +1 for the newline
	}
	return -1
}
