// Package generator handles template execution and file writing for the HAMR CLI.
package generator

import (
	"bufio"
	"embed"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"text/template"
)

//go:embed templates/new
var newTemplates embed.FS

// renderFromFS renders a template from the given embedded FS and writes the
// result to destPath, creating parent directories as needed.
func renderFromFS(fsys embed.FS, tmplPath, destPath string, data any) error {
	content, err := fsys.ReadFile(tmplPath)
	if err != nil {
		return fmt.Errorf("read template %s: %w", tmplPath, err)
	}

	tmpl, err := template.New(filepath.Base(tmplPath)).Parse(string(content))
	if err != nil {
		return fmt.Errorf("parse template %s: %w", tmplPath, err)
	}

	if err := os.MkdirAll(filepath.Dir(destPath), 0o755); err != nil {
		return fmt.Errorf("create directory: %w", err)
	}

	f, err := os.Create(destPath)
	if err != nil {
		return fmt.Errorf("create file: %w", err)
	}
	defer func() { _ = f.Close() }()

	return tmpl.Execute(f, data)
}

func parseGoMod(path string) (module, goVersion string, err error) {
	f, err := os.Open(path)
	if err != nil {
		return "", "", err
	}
	defer func() { _ = f.Close() }()

	scanner := bufio.NewScanner(f)
	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())
		if strings.HasPrefix(line, "module ") {
			module = strings.TrimPrefix(line, "module ")
		}
		if strings.HasPrefix(line, "go ") {
			goVersion = strings.TrimPrefix(line, "go ")
		}
	}
	if err := scanner.Err(); err != nil {
		return "", "", err
	}
	if module == "" {
		return "", "", fmt.Errorf("module directive not found in %s", path)
	}
	if goVersion == "" {
		return "", "", fmt.Errorf("go directive not found in %s", path)
	}
	return module, goVersion, nil
}
