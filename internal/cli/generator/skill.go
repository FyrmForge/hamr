package generator

import (
	"bytes"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"text/template"
)

// SupportedSkillTargets lists the AI agents we currently publish a skill for.
// Extend this list when adding new targets (codex, opencode, etc.).
var SupportedSkillTargets = []string{"claude"}

// SkillData is the render context passed to skill templates. Fields mirror
// scaffold options that the skill's guidance depends on.
type SkillData struct {
	IncludeAlpine bool
}

// InstallSkill copies the embedded skill tree for target into destDir.
// Files with a .tmpl suffix are rendered as Go text/template (with [[ ]]
// delimiters so they don't collide with templ's {{ ... }} references) and
// written with the suffix stripped. All other files are copied verbatim.
// When destDir already exists and force is false, it returns an error.
// When force is true, the existing destDir is removed before writing.
func InstallSkill(target, destDir string, force bool, data SkillData) error {
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

		if strings.HasSuffix(rel, ".tmpl") {
			rendered, err := renderSkillTemplate(path, content, data)
			if err != nil {
				return err
			}
			content = rendered
			rel = strings.TrimSuffix(rel, ".tmpl")
		}

		return copyRawFile(content, filepath.Join(destDir, rel))
	})
}

// renderSkillTemplate parses a skill template with [[ ]] delimiters (so the
// many literal {{ ... }} references inside skill markdown stay untouched) and
// returns the rendered bytes.
func renderSkillTemplate(path string, content []byte, data SkillData) ([]byte, error) {
	tmpl, err := template.New(filepath.Base(path)).Delims("[[", "]]").Parse(string(content))
	if err != nil {
		return nil, fmt.Errorf("parse skill template %s: %w", path, err)
	}
	var buf bytes.Buffer
	if err := tmpl.Execute(&buf, data); err != nil {
		return nil, fmt.Errorf("render skill template %s: %w", path, err)
	}
	return buf.Bytes(), nil
}
