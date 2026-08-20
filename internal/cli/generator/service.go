package generator

import (
	"bytes"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"text/template"
)

// ServiceTypes lists the supported `hamr add service` types.
var ServiceTypes = []string{"empty", "worker", "api", "html"}

// ServiceConfig holds the data used to render service templates.
type ServiceConfig struct {
	Name        string // service name, e.g. "billing" — used for cmd/<name>, internal/<name>, bin/<name>
	Type        string // "empty" | "worker" | "api" | "html"
	Module      string // project module path from go.mod
	GoVersion   string // from go.mod, used in the Dockerfile base image
	ProjectSlug string // slug of the project directory name, for sqlite defaults
	Port        int    // listen port (api/html only)
	WithDB      bool   // wire the project's repo store
	WithAuth    bool   // wire session auth middleware (api/html, requires WithDB)
	WithLocale  bool   // wire the locale bundle (html only)

	// HasTemplRule is true when the project's hamr.toml has a [[dev.watch]]
	// rule named "templ". Only then does an html service's watch rule get
	// depends = ["templ"] — an unresolvable dependency is a hard config error
	// that stops hamr dev entirely.
	HasTemplRule bool

	// Copied from the project's hamr.toml [options] so DB wiring matches.
	Database      string // "postgres" | "sqlite"
	DBConnector   string // "sqlx" | "gorm"
	DefaultLocale string // from [locale].default, "en" fallback
}

// PkgName returns the Go package name for internal/<name>: lowercase with
// separators stripped ("billing-svc" → "billingsvc").
func (cfg *ServiceConfig) PkgName() string {
	var b strings.Builder
	for _, r := range strings.ToLower(cfg.Name) {
		if (r >= 'a' && r <= 'z') || (r >= '0' && r <= '9') {
			b.WriteRune(r)
		}
	}
	return b.String()
}

// EnvPrefix returns the env-var prefix for the service ("billing-svc" → "BILLING_SVC").
func (cfg *ServiceConfig) EnvPrefix() string {
	var b strings.Builder
	prevSep := false
	for _, r := range strings.ToUpper(cfg.Name) {
		switch {
		case (r >= 'A' && r <= 'Z') || (r >= '0' && r <= '9'):
			b.WriteRune(r)
			prevSep = false
		default:
			if b.Len() > 0 && !prevSep {
				b.WriteByte('_')
				prevSep = true
			}
		}
	}
	return strings.TrimSuffix(b.String(), "_")
}

// HTTP reports whether the service type listens on a port.
func (cfg *ServiceConfig) HTTP() bool {
	return cfg.Type == "api" || cfg.Type == "html"
}

// Validate checks the ServiceConfig and applies defaults.
func (cfg *ServiceConfig) Validate() error {
	if err := ValidateProjectName(cfg.Name); err != nil {
		return fmt.Errorf("service name %q: %w", cfg.Name, err)
	}
	valid := false
	for _, t := range ServiceTypes {
		if cfg.Type == t {
			valid = true
		}
	}
	if !valid {
		return fmt.Errorf("invalid service type %q (supported: %s)", cfg.Type, strings.Join(ServiceTypes, ", "))
	}
	if cfg.Module == "" {
		return fmt.Errorf("module path is required")
	}
	if cfg.GoVersion == "" {
		return fmt.Errorf("go version is required")
	}
	if cfg.Type == "empty" {
		cfg.WithDB = false
	}
	if cfg.WithAuth && !cfg.HTTP() {
		return fmt.Errorf("auth is only supported for api and html services")
	}
	if cfg.WithAuth && !cfg.WithDB {
		return fmt.Errorf("auth requires the database (sessions live in the store)")
	}
	if cfg.WithLocale && cfg.Type != "html" {
		return fmt.Errorf("locale is only supported for html services")
	}
	if cfg.HTTP() && cfg.Port == 0 {
		cfg.Port = 8081
	}
	if cfg.HTTP() && (cfg.Port < 1 || cfg.Port > 65535) {
		return fmt.Errorf("invalid port %d: must be 1-65535", cfg.Port)
	}
	if cfg.Database == "" {
		cfg.Database = "postgres"
	}
	if cfg.DBConnector == "" {
		cfg.DBConnector = "sqlx"
	}
	if cfg.DefaultLocale == "" {
		cfg.DefaultLocale = "en"
	}
	if cfg.ProjectSlug == "" {
		cfg.ProjectSlug = "app"
	}
	return nil
}

// GenerateService scaffolds a new service into an existing project at dir:
// cmd/<name>/ (main.go + Dockerfile), internal/<name>/, a [[dev.watch]] block
// appended to hamr.toml, and (for HTTP types) a port line appended to .env and
// .env.example.
func GenerateService(dir string, cfg *ServiceConfig) error {
	cmdDir := filepath.Join(dir, "cmd", cfg.Name)
	internalDir := filepath.Join(dir, "internal", cfg.Name)
	for _, d := range []string{cmdDir, internalDir} {
		if _, err := os.Stat(d); err == nil {
			return fmt.Errorf("%s already exists", d)
		}
	}

	files := buildServiceFileList(cfg)

	for _, f := range files {
		if err := renderFromFS(templates, f.tmpl, filepath.Join(dir, f.dest), cfg); err != nil {
			_ = os.RemoveAll(cmdDir)
			_ = os.RemoveAll(internalDir)
			return fmt.Errorf("render %s: %w", f.dest, err)
		}
	}

	// Append the [[dev.watch]] rule so `hamr dev` builds and runs the service.
	if err := appendRendered(templates, "templates/service/hamr_toml.tmpl", filepath.Join(dir, "hamr.toml"), cfg, false); err != nil {
		return fmt.Errorf("append hamr.toml: %w", err)
	}

	// Append the port env var for HTTP services. Missing env files are skipped
	// (projects may not keep a .env in git).
	if cfg.HTTP() {
		for _, envFile := range []string{".env", ".env.example"} {
			if err := appendRendered(templates, "templates/service/env.tmpl", filepath.Join(dir, envFile), cfg, true); err != nil {
				return fmt.Errorf("append %s: %w", envFile, err)
			}
		}
	}

	return nil
}

func buildServiceFileList(cfg *ServiceConfig) []templateFile {
	n := cfg.Name
	files := []templateFile{
		{"templates/service/main_" + cfg.Type + ".go.tmpl", filepath.Join("cmd", n, "main.go")},
		{"templates/service/Dockerfile.tmpl", filepath.Join("cmd", n, "Dockerfile")},
	}
	switch cfg.Type {
	case "empty":
		files = append(files, templateFile{"templates/service/empty.go.tmpl", filepath.Join("internal", n, n+".go")})
	case "worker":
		files = append(files, templateFile{"templates/service/worker.go.tmpl", filepath.Join("internal", n, "worker.go")})
	case "api":
		files = append(files, templateFile{"templates/service/api_server.go.tmpl", filepath.Join("internal", n, "api", "server.go")})
	case "html":
		files = append(files,
			templateFile{"templates/service/web_server.go.tmpl", filepath.Join("internal", n, "web", "server.go")},
			templateFile{"templates/service/web_home.go.tmpl", filepath.Join("internal", n, "web", "home.go")},
			templateFile{"templates/service/web_home.templ.tmpl", filepath.Join("internal", n, "web", "home.templ")},
		)
	}
	return files
}

// appendRendered renders tmplPath and appends the result to destPath.
// When skipMissing is true and destPath does not exist, it is a no-op.
func appendRendered(fsys interface{ ReadFile(string) ([]byte, error) }, tmplPath, destPath string, data any, skipMissing bool) error {
	if skipMissing {
		if _, err := os.Stat(destPath); os.IsNotExist(err) {
			return nil
		}
	}

	content, err := fsys.ReadFile(tmplPath)
	if err != nil {
		return fmt.Errorf("read template %s: %w", tmplPath, err)
	}
	tmpl, err := template.New(filepath.Base(tmplPath)).Funcs(template.FuncMap{
		"lower": strings.ToLower,
		"slug":  ProjectSlug,
	}).Parse(string(content))
	if err != nil {
		return fmt.Errorf("parse template %s: %w", tmplPath, err)
	}
	var buf bytes.Buffer
	if err := tmpl.Execute(&buf, data); err != nil {
		return fmt.Errorf("render template %s: %w", tmplPath, err)
	}

	f, err := os.OpenFile(destPath, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o644)
	if err != nil {
		return err
	}
	defer func() { _ = f.Close() }()
	_, err = f.Write(buf.Bytes())
	return err
}
