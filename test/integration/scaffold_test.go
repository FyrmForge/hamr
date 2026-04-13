//go:build integration

package integration

import (
	"fmt"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"

	"github.com/FyrmForge/hamr/internal/cli/generator"
	"github.com/stretchr/testify/require"
)

var hamrBin string

func TestMain(m *testing.M) {
	if err := exec.Command("docker", "info").Run(); err != nil {
		fmt.Println("skipping integration tests: docker not available")
		os.Exit(0)
	}
	if _, err := exec.LookPath("templ"); err != nil {
		fmt.Println("skipping integration tests: templ not on PATH")
		os.Exit(0)
	}

	tmp, err := os.MkdirTemp("", "hamr-inttest-*")
	if err != nil {
		fmt.Printf("failed to create temp dir: %v\n", err)
		os.Exit(1)
	}
	defer os.RemoveAll(tmp)

	hamrBin = filepath.Join(tmp, "hamr")
	cmd := exec.Command("go", "build", "-o", hamrBin, "./cmd/hamr")
	cmd.Dir = repoRoot()
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	if err := cmd.Run(); err != nil {
		fmt.Printf("failed to build hamr: %v\n", err)
		os.Exit(1)
	}

	os.Exit(m.Run())
}

func repoRoot() string {
	_, f, _, _ := runtime.Caller(0)
	return filepath.Join(filepath.Dir(f), "..", "..")
}

// ---------------------------------------------------------------------------
// Presets
// ---------------------------------------------------------------------------

type preset struct {
	name string
	cfg  *generator.ProjectConfig
	port int
}

func presets() []preset {
	goVer := generator.DetectGoVersion()
	return []preset{
		{
			name: "minimal",
			port: 18080,
			cfg: &generator.ProjectConfig{
				Name:        "minimaltest",
				Module:      "github.com/test/minimaltest",
				CSS:         "plain",
				DBConnector: "sqlx",
				GoVersion:   goVer,
			},
		},
		{
			name: "full",
			port: 18081,
			cfg: &generator.ProjectConfig{
				Name:           "fulltest",
				Module:         "github.com/test/fulltest",
				CSS:            "tailwind",
				DBConnector:    "sqlx",
				GoVersion:      goVer,
				IncludeAuth:    true,
				IncludeWS:      true,
				IncludeE2E:     true,
				IncludeStripe:  true,
				IncludeLocale:  true,
				StorageBackend: "s3",
				IncludePgAdmin: true,
			},
		},
		{
			name: "gorm",
			port: 18082,
			cfg: &generator.ProjectConfig{
				Name:             "gormtest",
				Module:           "github.com/test/gormtest",
				CSS:              "plain",
				DBConnector:      "gorm",
				MigrateAtStartup: true,
				GoVersion:        goVer,
				IncludeAuth:      true,
				StorageBackend:   "local",
			},
		},
	}
}

// ---------------------------------------------------------------------------
// Harness
// ---------------------------------------------------------------------------

type harness struct {
	t      *testing.T
	dir    string
	cfg    *generator.ProjectConfig
	port   int
	srvCmd *exec.Cmd
	exited chan error // closed when server process exits
}

func (h *harness) scaffold() {
	h.t.Helper()
	require.NoError(h.t, h.cfg.Validate())
	require.NoError(h.t, generator.GenerateProject(h.dir, h.cfg))
}

func (h *harness) injectReplace() {
	h.t.Helper()
	gomod := filepath.Join(h.dir, "go.mod")
	f, err := os.OpenFile(gomod, os.O_APPEND|os.O_WRONLY, 0o644)
	require.NoError(h.t, err)
	defer f.Close()
	_, err = fmt.Fprintf(f, "\nreplace github.com/FyrmForge/hamr => %s\n", repoRoot())
	require.NoError(h.t, err)
}

func (h *harness) prepareEnv() {
	h.t.Helper()
	envFile := filepath.Join(h.dir, ".env")
	data, err := os.ReadFile(envFile)
	require.NoError(h.t, err)
	updated := strings.Replace(string(data), "PORT=8080", fmt.Sprintf("PORT=%d", h.port), 1)
	require.NoError(h.t, os.WriteFile(envFile, []byte(updated), 0o644))
}

func (h *harness) templGenerate() {
	h.t.Helper()
	h.runInProject("templ", "generate")
}

func (h *harness) localeGenerate() {
	h.t.Helper()
	if !h.cfg.IncludeLocale {
		return
	}
	h.runInProject(hamrBin, "locale", "gen")
}

func (h *harness) goModTidy() {
	h.t.Helper()
	h.runInProject("go", "get", "./...")
	h.runInProject("go", "mod", "tidy")
}

func (h *harness) npmInstall() {
	h.t.Helper()
	if h.cfg.CSS != "tailwind" {
		return
	}
	h.runInProject("npm", "install")
}

func (h *harness) composeUp() {
	h.t.Helper()
	project := "inttest-" + h.cfg.Name
	h.runInProject("docker", "compose",
		"-f", "docker/docker-compose.yaml",
		"-p", project,
		"up", "-d", "--wait")
	h.t.Cleanup(func() {
		cmd := exec.Command("docker", "compose",
			"-f", "docker/docker-compose.yaml",
			"-p", project,
			"down", "-v", "--remove-orphans")
		cmd.Dir = h.dir
		cmd.Stdout = &testWriter{h.t}
		cmd.Stderr = &testWriter{h.t}
		if err := cmd.Run(); err != nil {
			h.t.Logf("compose down failed: %v", err)
		}
	})
}

func (h *harness) build() {
	h.t.Helper()
	h.runInProject("make", "build")
}

func (h *harness) migrate() {
	h.t.Helper()
	if h.cfg.MigrateAtStartup {
		return
	}
	h.runInProject("make", "migrate")
}

func (h *harness) startServer() {
	h.t.Helper()
	bin := filepath.Join(h.dir, "bin", "site")
	h.srvCmd = exec.Command(bin)
	h.srvCmd.Dir = h.dir
	h.srvCmd.Env = append(os.Environ(),
		fmt.Sprintf("PORT=%d", h.port),
		"PATH="+filepath.Dir(hamrBin)+":"+os.Getenv("PATH"),
	)
	h.srvCmd.Stdout = &testWriter{h.t}
	h.srvCmd.Stderr = &testWriter{h.t}
	require.NoError(h.t, h.srvCmd.Start())

	// Monitor for early exit so waitHealthy can fail fast.
	h.exited = make(chan error, 1)
	go func() { h.exited <- h.srvCmd.Wait() }()

	h.t.Cleanup(func() {
		_ = h.srvCmd.Process.Kill()
		<-h.exited // drain the wait goroutine
	})
}

func (h *harness) waitHealthy() {
	h.t.Helper()
	url := fmt.Sprintf("http://localhost:%d/api/health", h.port)
	deadline := time.Now().Add(30 * time.Second)
	var lastErr string
	for time.Now().Before(deadline) {
		select {
		case err := <-h.exited:
			h.t.Fatalf("server exited before becoming healthy: %v", err)
		default:
		}
		resp, err := http.Get(url)
		if err != nil {
			lastErr = err.Error()
		} else {
			resp.Body.Close()
			if resp.StatusCode == http.StatusOK {
				return
			}
			lastErr = fmt.Sprintf("status %d", resp.StatusCode)
		}
		time.Sleep(500 * time.Millisecond)
	}
	h.t.Fatalf("server not healthy within 30s: %s (last: %s)", url, lastErr)
}

func (h *harness) runMakeTest() {
	h.t.Helper()
	h.runInProject("make", "test")
}

// ---------------------------------------------------------------------------
// Helpers
// ---------------------------------------------------------------------------

func (h *harness) runInProject(name string, args ...string) {
	h.t.Helper()
	cmd := exec.Command(name, args...)
	cmd.Dir = h.dir
	cmd.Env = append(os.Environ(),
		"PATH="+filepath.Dir(hamrBin)+":"+os.Getenv("PATH"),
	)
	cmd.Stdout = &testWriter{h.t}
	cmd.Stderr = &testWriter{h.t}
	require.NoError(h.t, cmd.Run(), "command failed: %s %s", name, strings.Join(args, " "))
}

type testWriter struct {
	t *testing.T
}

func (tw *testWriter) Write(p []byte) (int, error) {
	tw.t.Helper()
	tw.t.Log(strings.TrimRight(string(p), "\n"))
	return len(p), nil
}

// ---------------------------------------------------------------------------
// Tests
// ---------------------------------------------------------------------------

func TestScaffold_Startup(t *testing.T) {
	for _, p := range presets() {
		t.Run(p.name, func(t *testing.T) {
			h := &harness{
				t:    t,
				dir:  filepath.Join(t.TempDir(), p.cfg.Name),
				cfg:  p.cfg,
				port: p.port,
			}

			h.scaffold()
			h.injectReplace()
			h.prepareEnv()
			h.templGenerate()
			h.localeGenerate()
			h.goModTidy()
			h.npmInstall()
			h.build()
			h.composeUp()
			h.migrate()
			h.startServer()
			h.waitHealthy()
		})
	}
}

func TestScaffold_GeneratedTests(t *testing.T) {
	for _, p := range presets() {
		t.Run(p.name, func(t *testing.T) {
			h := &harness{
				t:    t,
				dir:  filepath.Join(t.TempDir(), p.cfg.Name),
				cfg:  p.cfg,
				port: p.port,
			}

			h.scaffold()
			h.injectReplace()
			h.prepareEnv()
			h.templGenerate()
			h.localeGenerate()
			h.goModTidy()
			h.npmInstall()
			h.composeUp()
			h.runMakeTest()
		})
	}
}
