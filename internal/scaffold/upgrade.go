package scaffold

import (
	"fmt"
	"os"
	"regexp"
	"strings"
)

// UpgradeReport is the structured output of the upgrade command.
type UpgradeReport struct {
	Project ProjectInfo    `json:"project"`
	Changes []ReportChange `json:"changes"`
}

// ProjectInfo describes the project's scaffold state.
type ProjectInfo struct {
	BaseVersion    string  `json:"base_version"`
	CurrentVersion string  `json:"current_version"`
	ScaffoldedAt   string  `json:"scaffolded_at,omitempty"`
	Options        Options `json:"options"`
}

// ReportChange is a Change annotated with relevance information.
type ReportChange struct {
	Change
	Relevant        bool   `json:"relevant"`
	RelevanceReason string `json:"relevance_reason,omitempty"`
}

// ReportFilters controls which changes appear in the report.
type ReportFilters struct {
	Category    Category
	FromVersion string
	RelevantOnly bool
}

// BuildReport produces an upgrade report comparing the project's base version
// to the current HAMR version. The allChanges parameter is accepted for testability.
func BuildReport(meta Metadata, currentVersion string, allChanges []Change, filters ReportFilters) (*UpgradeReport, error) {
	baseVersionStr := meta.Hamr.Version
	if filters.FromVersion != "" {
		baseVersionStr = filters.FromVersion
	}

	baseVer, err := ParseVersion(baseVersionStr)
	if err != nil {
		return nil, fmt.Errorf("parse base version: %w", err)
	}

	currentVer, err := ParseVersion(currentVersion)
	if err != nil {
		return nil, fmt.Errorf("parse current version: %w", err)
	}

	report := &UpgradeReport{
		Project: ProjectInfo{
			BaseVersion:    baseVer.String(),
			CurrentVersion: currentVer.String(),
			ScaffoldedAt:   meta.Hamr.ScaffoldedAt,
			Options:        meta.Options,
		},
	}

	for _, c := range allChanges {
		sinceVer, err := ParseVersion(c.Since)
		if err != nil {
			continue // skip changes with unparseable versions
		}

		// Only include changes newer than the base version.
		if !baseVer.Less(sinceVer) {
			continue
		}

		// Only include changes up to and including the current version.
		if currentVer.Less(sinceVer) {
			continue
		}

		// Filter by category if specified.
		if filters.Category != "" && c.Category != filters.Category {
			continue
		}

		relevant := c.IsRelevant(meta.Options)
		reason := ""
		if !relevant {
			reason = relevanceReason(c, meta.Options)
		}

		if filters.RelevantOnly && !relevant {
			continue
		}

		report.Changes = append(report.Changes, ReportChange{
			Change:          c,
			Relevant:        relevant,
			RelevanceReason: reason,
		})
	}

	// Ensure Changes is never nil for consistent JSON output.
	if report.Changes == nil {
		report.Changes = []ReportChange{}
	}

	return report, nil
}

// relevanceReason returns a human-readable explanation for why a change is not relevant.
func relevanceReason(c Change, opts Options) string {
	var disabled []string
	for _, key := range c.RelevantOptions {
		if !optionEnabled(opts, key) {
			disabled = append(disabled, key)
		}
	}
	if len(disabled) > 0 {
		return fmt.Sprintf("%s not enabled in project options", strings.Join(disabled, ", "))
	}
	return ""
}

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
