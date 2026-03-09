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

// StaticPage registers a handler for build-time static generation.
// It stores the path+handler for GenerateStatic but does NOT register a route.
// The route should be registered separately via site.GET for runtime fallback.
func (s *Server) StaticPage(path string, handler echo.HandlerFunc) {
	s.staticPages = append(s.staticPages, staticRoute{path: path, handler: handler})
}

// GenerateStatic renders all registered static pages to files in dir.
// Path mapping: "/" → dir/index.html, "/about" → dir/about/index.html.
func (s *Server) GenerateStatic(dir string) error {
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

// generatedMiddleware serves pre-rendered static pages from dir.
// Only intercepts GET requests where dir/<path>/index.html exists on disk.
func generatedMiddleware(dir string) echo.MiddlewareFunc {
	return func(next echo.HandlerFunc) echo.HandlerFunc {
		return func(c echo.Context) error {
			if c.Request().Method != http.MethodGet {
				return next(c)
			}

			file := staticPathToFile(dir, c.Request().URL.Path)
			if _, err := os.Stat(file); err != nil {
				return next(c)
			}
			return c.File(file)
		}
	}
}
