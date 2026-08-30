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

// SupportedSkillTargets lists the AI agents skills can be installed for. The
// SKILL.md format is a cross-tool standard, so targets differ only in where
// the files go: claude reads .claude/skills/, codex and opencode read the
// shared .agents/skills/ (opencode reads .claude/skills/ too).
var SupportedSkillTargets = []string{"claude", "codex", "opencode"}

// SkillNames lists the skills we publish, in install/picker order. Each maps to
// a directory under templates/skills/<target>/<name>.
var SkillNames = []string{"hamr", "qa-loop", "pr-publish"}

// SkillDirName is the directory a skill installs into under .claude/skills/.
// The framework skill keeps the bare "hamr" name it has always had; the
// workflow skills are prefixed so they don't squat generic names.
func SkillDirName(skill string) string {
	if skill == "hamr" {
		return "hamr"
	}
	return "hamr-" + skill
}

// SkillData is the render context passed to skill templates. Fields mirror
// scaffold options that the skill's guidance depends on.
type SkillData struct {
	IncludeAlpine bool
}

// InstallSkill copies the embedded tree for one skill into destDir. The tree
// is target-independent (the SKILL.md format is a cross-tool standard).
// Files with a .tmpl suffix are rendered as Go text/template (with [[ ]]
// delimiters so they don't collide with templ's {{ ... }} references) and
// written with the suffix stripped. All other files are copied verbatim.
// When destDir already exists and force is false, it returns an error.
// When force is true, the existing destDir is removed before writing.
func InstallSkill(skill, destDir string, force bool, data SkillData) error {
	src := filepath.ToSlash(filepath.Join("templates", "skills", skill))
	if _, err := fs.Stat(templates, src); err != nil {
		return fmt.Errorf("unknown skill %q", skill)
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
