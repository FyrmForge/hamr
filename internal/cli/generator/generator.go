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

//go:embed templates/service
var serviceTemplates embed.FS

//go:embed templates/project
var projectTemplates embed.FS

// ServiceConfig holds the data used to render service templates.
type ServiceConfig struct {
	Name      string // "billing"
	Module    string // inherited from project's go.mod
	GoVersion string // inherited from project's go.mod
	Storage   string // "" (none), "local", or "s3"
}

// NewServiceConfigFromProject reads go.mod in the current directory and
// builds a ServiceConfig for the named service.
func NewServiceConfigFromProject(name string) (*ServiceConfig, error) {
	module, goVersion, err := parseGoMod("go.mod")
	if err != nil {
		return nil, fmt.Errorf("parse go.mod: %w", err)
	}
	return &ServiceConfig{
		Name:      name,
		Module:    module,
		GoVersion: goVersion,
	}, nil
}

// GenerateService scaffolds a new service into the current project directory.
func GenerateService(cfg *ServiceConfig) error {
	files := []struct {
		tmpl string // path inside embedded FS
		dest string // output path relative to project root
	}{
		{"templates/service/cmd_main.go.tmpl", filepath.Join("cmd", cfg.Name, "main.go")},
		{"templates/service/cmd_dockerfile.tmpl", filepath.Join("cmd", cfg.Name, "Dockerfile")},
		{"templates/service/internal_config.go.tmpl", filepath.Join("internal", cfg.Name, "config", "config.go")},
		{"templates/service/internal_handler_health.go.tmpl", filepath.Join("internal", cfg.Name, "handler", "health.go")},
	}

	for _, f := range files {
		if err := renderTemplate(f.tmpl, f.dest, cfg); err != nil {
			return fmt.Errorf("render %s: %w", f.dest, err)
		}
	}

	if err := ensureProjectFiles(cfg); err != nil {
		return fmt.Errorf("ensure project files: %w", err)
	}

	if err := appendDockerCompose(cfg); err != nil {
		return fmt.Errorf("update docker-compose: %w", err)
	}

	if err := appendMakefile(cfg); err != nil {
		return fmt.Errorf("update Makefile: %w", err)
	}

	if cfg.Storage == "s3" {
		if err := appendMinIO(cfg); err != nil {
			return fmt.Errorf("add MinIO service: %w", err)
		}
	}

	return nil
}

func renderTemplate(tmplPath, destPath string, data any) error {
	content, err := serviceTemplates.ReadFile(tmplPath)
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

	if _, err := os.Stat(destPath); err == nil {
		return fmt.Errorf("file already exists: %s", destPath)
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

func appendDockerCompose(cfg *ServiceConfig) error {
	const path = "docker/docker-compose.yaml"
	if _, err := os.Stat(path); os.IsNotExist(err) {
		return nil // no docker-compose to update
	}

	snippet := fmt.Sprintf(`
  %s:
    build:
      context: ../
      dockerfile: cmd/%s/Dockerfile
    ports:
      - "${%s_PORT:-8080}:${%s_PORT:-8080}"
    environment:
      - PORT=${%s_PORT:-8080}
    restart: unless-stopped
`,
		cfg.Name,
		cfg.Name,
		strings.ToUpper(cfg.Name),
		strings.ToUpper(cfg.Name),
		strings.ToUpper(cfg.Name),
	)

	f, err := os.OpenFile(path, os.O_APPEND|os.O_WRONLY, 0o644)
	if err != nil {
		return err
	}
	defer func() { _ = f.Close() }()

	_, err = f.WriteString(snippet)
	return err
}

func appendMakefile(cfg *ServiceConfig) error {
	const path = "Makefile"
	if _, err := os.Stat(path); os.IsNotExist(err) {
		return nil // no Makefile to update
	}

	snippet := fmt.Sprintf(`
.PHONY: run-%s build-%s

run-%s:
	go run ./cmd/%s

build-%s:
	go build -o bin/%s ./cmd/%s
`,
		cfg.Name, cfg.Name,
		cfg.Name, cfg.Name,
		cfg.Name, cfg.Name, cfg.Name,
	)

	f, err := os.OpenFile(path, os.O_APPEND|os.O_WRONLY, 0o644)
	if err != nil {
		return err
	}
	defer func() { _ = f.Close() }()

	_, err = f.WriteString(snippet)
	return err
}

func ensureProjectFiles(cfg *ServiceConfig) error {
	files := []struct {
		tmpl string // path inside embedded FS
		dest string // output path relative to project root
	}{
		{"templates/project/golangci.yml.tmpl", ".golangci.yml"},
	}

	for _, f := range files {
		if _, err := os.Stat(f.dest); err == nil {
			continue // already exists, skip
		}
		if err := renderProjectTemplate(f.tmpl, f.dest, cfg); err != nil {
			return fmt.Errorf("render %s: %w", f.dest, err)
		}
	}
	return nil
}

func appendMinIO(_ *ServiceConfig) error {
	const path = "docker/docker-compose.yaml"
	if _, err := os.Stat(path); os.IsNotExist(err) {
		return nil // no docker-compose to update
	}

	data, err := os.ReadFile(path)
	if err != nil {
		return err
	}
	content := string(data)

	// Idempotent: skip if MinIO is already present.
	if strings.Contains(content, "minio:") {
		return nil
	}

	f, err := os.OpenFile(path, os.O_APPEND|os.O_WRONLY, 0o644)
	if err != nil {
		return err
	}
	defer func() { _ = f.Close() }()

	snippet := `
  minio:
    image: minio/minio:latest
    command: server /data --console-address ":9001"
    ports:
      - "9000:9000"
      - "9001:9001"
    environment:
      - MINIO_ROOT_USER=minioadmin
      - MINIO_ROOT_PASSWORD=minioadmin
    volumes:
      - minio_data:/data
    restart: unless-stopped
`
	if _, err := f.WriteString(snippet); err != nil {
		return err
	}

	// Append named volume if not already present.
	if !strings.Contains(content, "minio_data:") {
		if strings.Contains(content, "\nvolumes:") {
			// A volumes section already exists; append just the entry.
			if _, err := f.WriteString("  minio_data:\n"); err != nil {
				return err
			}
		} else {
			if _, err := f.WriteString("\nvolumes:\n  minio_data:\n"); err != nil {
				return err
			}
		}
	}

	return nil
}

func renderProjectTemplate(tmplPath, destPath string, data any) error {
	content, err := projectTemplates.ReadFile(tmplPath)
	if err != nil {
		return fmt.Errorf("read template %s: %w", tmplPath, err)
	}

	tmpl, err := template.New(filepath.Base(tmplPath)).Parse(string(content))
	if err != nil {
		return fmt.Errorf("parse template %s: %w", tmplPath, err)
	}

	f, err := os.Create(destPath)
	if err != nil {
		return fmt.Errorf("create file: %w", err)
	}
	defer func() { _ = f.Close() }()

	return tmpl.Execute(f, data)
}
