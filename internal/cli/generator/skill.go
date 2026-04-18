package generator

import (
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
)

// SupportedSkillTargets lists the AI agents we currently publish a skill for.
// Extend this list when adding new targets (codex, opencode, etc.).
var SupportedSkillTargets = []string{"claude"}

// InstallSkill copies the embedded skill tree for target into destDir.
// When destDir already exists and force is false, it returns an error.
// When force is true, the existing destDir is removed before writing.
func InstallSkill(target, destDir string, force bool) error {
	src := filepath.ToSlash(filepath.Join("templates", "skills", target))
	if _, err := fs.Stat(templates, src); err != nil {
		return fmt.Errorf("unknown skill target %q", target)
	}

	if _, err := os.Stat(destDir); err == nil {
		if !force {
			return fmt.Errorf("%s already exists (pass --force to overwrite)", destDir)
		}
		if err := os.RemoveAll(destDir); err != nil {
			return fmt.Errorf("remove existing skill dir: %w", err)
		}
	} else if !errors.Is(err, os.ErrNotExist) {
		return fmt.Errorf("stat destination: %w", err)
	}

	return fs.WalkDir(templates, src, func(path string, d fs.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		if d.IsDir() {
			return nil
		}
		rel, err := filepath.Rel(src, path)
		if err != nil {
			return err
		}
		content, err := templates.ReadFile(path)
		if err != nil {
			return fmt.Errorf("read %s: %w", path, err)
		}
		return copyRawFile(content, filepath.Join(destDir, rel))
	})
}
