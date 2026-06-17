package scaffold

import (
	"bytes"
	"context"
	"fmt"
	"os"
	"os/exec"
	"strings"
)

// HamrRepoURL is the default repository URL for diffing scaffold changes.
const HamrRepoURL = "https://github.com/FyrmForge/hamr.git"

// upgradeDiffPaths is the git pathspec the upgrade diff is scoped to — what a
// downstream project actually consumes or mirrors:
//   - pkg/                              the importable libraries
//   - internal/cli/generator/templates the scaffold the project's files come
//     from (so an agent can upgrade the project to the latest template)
//   - docs/                            the project carries its own docs (the
//     scaffold templates a docs/ tree, CLAUDE.md, AGENTS.md, skill references),
//     so an agent can bring them up to date
//   - llmsdocs/                        the project's llms.txt-style AI context
//     tracks these
//
// Only hamr's own internal tooling — the CLI commands, the dev server, the
// generator logic (NOT its templates), the hamr binary, and tests — is excluded:
// it can never appear in, or be applied to, a scaffolded project.
var upgradeDiffPaths = []string{
	"pkg",
	"internal/cli/generator/templates",
	"docs",
	"llmsdocs",
	":(exclude)**/*_test.go",
}

// DiffReport is the structured output of the git-based upgrade command.
type DiffReport struct {
	Project  DiffProjectInfo `json:"project"`
	Diff     string          `json:"diff"`
	DiffStat string          `json:"diff_stat"`
}

// DiffProjectInfo describes the project's scaffold state for a diff report.
type DiffProjectInfo struct {
	BaseVersion    string  `json:"base_version"`
	CurrentVersion string  `json:"current_version"`
	ScaffoldedAt   string  `json:"scaffolded_at,omitempty"`
	Options        Options `json:"options"`
}

// GitDiff clones the given repo (bare, partial) and produces a unified diff
// between two version tags. The repoURL parameter allows tests to pass a
// local repository path instead of hitting the network.
func GitDiff(ctx context.Context, repoURL, baseVersion, currentVersion string) (*DiffReport, error) {
	if _, err := exec.LookPath("git"); err != nil {
		return nil, fmt.Errorf("git not found in PATH: %w", err)
	}

	tmpDir, err := os.MkdirTemp("", "hamr-diff-*")
	if err != nil {
		return nil, fmt.Errorf("create temp dir: %w", err)
	}
	defer os.RemoveAll(tmpDir) //nolint:errcheck

	// Clone bare with partial blob filter for speed.
	if err := gitRun(ctx, tmpDir, "clone", "--bare", "--filter=blob:none", repoURL, tmpDir); err != nil {
		return nil, fmt.Errorf("clone repo: %w", err)
	}

	baseTag := "v" + strings.TrimPrefix(baseVersion, "v")
	currentTag := "v" + strings.TrimPrefix(currentVersion, "v")

	// Validate tags exist.
	if err := gitTagExists(ctx, tmpDir, baseTag); err != nil {
		return nil, fmt.Errorf("base version tag %s not found: %w", baseTag, err)
	}
	if err := gitTagExists(ctx, tmpDir, currentTag); err != nil {
		return nil, fmt.Errorf("current version tag %s not found: %w", currentTag, err)
	}

	// Scope the diff to what a downstream project actually consumes: the
	// importable libraries and the scaffold templates its own files were
	// generated from, plus the curated changelog. hamr's CLI / dev-server
	// internals, cmd/, and tests never appear in a scaffolded project, so
	// including them only buries the relevant changes in noise (and tokens) for
	// the agent doing the upgrade.
	rng := baseTag + ".." + currentTag

	// Get the unified diff.
	diff, err := gitOutput(ctx, tmpDir, append([]string{"diff", rng, "--"}, upgradeDiffPaths...)...)
	if err != nil {
		return nil, fmt.Errorf("git diff: %w", err)
	}

	// Get the stat summary.
	stat, err := gitOutput(ctx, tmpDir, append([]string{"diff", "--stat", rng, "--"}, upgradeDiffPaths...)...)
	if err != nil {
		return nil, fmt.Errorf("git diff --stat: %w", err)
	}

	return &DiffReport{
		Project: DiffProjectInfo{
			BaseVersion:    strings.TrimPrefix(baseVersion, "v"),
			CurrentVersion: strings.TrimPrefix(currentVersion, "v"),
		},
		Diff:     strings.TrimSpace(diff),
		DiffStat: strings.TrimSpace(stat),
	}, nil
}

// gitRun executes a git command and returns an error if it fails.
func gitRun(ctx context.Context, dir string, args ...string) error {
	cmd := exec.CommandContext(ctx, "git", args...)
	cmd.Dir = dir
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	if err := cmd.Run(); err != nil {
		return fmt.Errorf("%s: %s", err, strings.TrimSpace(stderr.String()))
	}
	return nil
}

// gitOutput executes a git command and returns its stdout.
func gitOutput(ctx context.Context, dir string, args ...string) (string, error) {
	cmd := exec.CommandContext(ctx, "git", args...)
	cmd.Dir = dir
	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr
	if err := cmd.Run(); err != nil {
		return "", fmt.Errorf("%s: %s", err, strings.TrimSpace(stderr.String()))
	}
	return stdout.String(), nil
}

// gitTagExists checks that a tag exists in the bare repo.
func gitTagExists(ctx context.Context, dir, tag string) error {
	out, err := gitOutput(ctx, dir, "tag", "-l", tag)
	if err != nil {
		return err
	}
	if strings.TrimSpace(out) == "" {
		return fmt.Errorf("tag %q does not exist", tag)
	}
	return nil
}
