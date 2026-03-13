package server

import (
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"

	"github.com/labstack/echo/v4"
)

// staticRoute holds a path and its handler for build-time generation.
type staticRoute struct {
	path    string
	handler echo.HandlerFunc
}

// StaticPage registers a handler for both build-time static generation and
// runtime serving. It stores the path+handler for GenerateStatic and registers
// a GET route that serves the pre-rendered file from the generated directory
// (if configured and the file exists), falling back to the handler otherwise.
func (s *Server) StaticPage(path string, handler echo.HandlerFunc) {
	s.staticPages = append(s.staticPages, staticRoute{path: path, handler: handler})

	s.echo.GET(path, func(c echo.Context) error {
		if s.generatedDir != "" {
			file := staticPathToFile(s.generatedDir, path)
			if _, err := os.Stat(file); err == nil {
				return c.File(file)
			}
		}
		return handler(c)
	})
}

// GenerateStatic renders all registered static pages to files in dir.
// Path mapping: "/" → dir/index.html, "/about" → dir/about/index.html.
// The output directory is wiped first so that stale files from previous
// runs (e.g. pages that were removed or renamed) do not linger.
func (s *Server) GenerateStatic(dir string) error {
	if err := os.RemoveAll(dir); err != nil {
		return fmt.Errorf("clean %s: %w", dir, err)
	}
	for _, sr := range s.staticPages {
		req := httptest.NewRequest(http.MethodGet, sr.path, nil)
		rec := httptest.NewRecorder()
		c := s.echo.NewContext(req, rec)

		if err := sr.handler(c); err != nil {
			return fmt.Errorf("generate %s: %w", sr.path, err)
		}

		dest := staticPathToFile(dir, sr.path)
		if err := os.MkdirAll(filepath.Dir(dest), 0o755); err != nil {
			return fmt.Errorf("mkdir for %s: %w", sr.path, err)
		}
		if err := os.WriteFile(dest, rec.Body.Bytes(), 0o644); err != nil {
			return fmt.Errorf("write %s: %w", dest, err)
		}
		fmt.Printf("[static] %s → %s\n", sr.path, dest)
	}
	return nil
}

// staticPathToFile maps a URL path to a file path inside dir.
//
//	"/"          → dir/index.html
//	"/about"     → dir/about/index.html
//	"/terms/privacy" → dir/terms/privacy/index.html
func staticPathToFile(dir, urlPath string) string {
	p := strings.TrimPrefix(urlPath, "/")
	if p == "" {
		return filepath.Join(dir, "index.html")
	}
	return filepath.Join(dir, p, "index.html")
}

