//go:build integration

package integration

import (
	"errors"
	"fmt"
	"net"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"runtime"
	"strconv"
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
}

func presets() []preset {
	goVer := generator.DetectGoVersion()
	return []preset{
		{
			name: "minimal",
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
			},
		},
		{
			name: "gorm",
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

	h.remapComposePorts()
}

// composePortLine matches a short-form published port in the scaffolded compose
// file, e.g. `      - "5432:5432"`.
var composePortLine = regexp.MustCompile(`(?m)^(\s*-\s*")(\d+):(\d+)(".*)$`)

// freePort asks the kernel for an unused loopback port.
//
// The listener is closed before the port is returned, so something else could
// in principle take it before the test binds. That window is far smaller than
// the problem it replaces: the scaffold hardcodes 5432 for postgres and
// 9000/9001 for the S3 mock, so any developer already running another
// project's containers on those ports could not run these tests at all.
func freePort(t *testing.T) int {
	t.Helper()
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	require.NoError(t, err)
	defer func() { _ = ln.Close() }()
	return ln.Addr().(*net.TCPAddr).Port
}

// remapComposePorts repoints every published host port in the scaffolded
// compose file at a free one, then rewrites the matching .env values so the app
// connects where the containers actually landed. Container-side ports are left
// alone — only the host half of each mapping moves.
//
// Without this the test does not merely fail to bind: an unrelated postgres
// already on 5432 makes compose fail, or worse, the app connects to THAT
// database and fails much later with a confusing "database does not exist".
func (h *harness) remapComposePorts() {
	h.t.Helper()

	composeFile := filepath.Join(h.dir, "docker", "docker-compose.yaml")
	data, err := os.ReadFile(composeFile)
	if errors.Is(err, os.ErrNotExist) {
		return // sqlite + local storage scaffolds ship no compose file
	}
	require.NoError(h.t, err)

	remapped := make(map[string]string)
	out := composePortLine.ReplaceAllStringFunc(string(data), func(line string) string {
		m := composePortLine.FindStringSubmatch(line)
		host, container := m[2], m[3]
		port := strconv.Itoa(freePort(h.t))
		remapped[host] = port
		h.t.Logf("compose port %s -> %s", host, port)
		return m[1] + port + ":" + container + m[4]
	})
	require.NoError(h.t, os.WriteFile(composeFile, []byte(out), 0o644))

	h.rewriteEnvPorts(remapped)
}

// rewriteEnvPorts points every ":<old>" in .env at its new host port. Done in
// one pass over an alternation rather than a replace per entry, so a new port
// that happens to equal another entry's old port cannot be rewritten twice.
func (h *harness) rewriteEnvPorts(remapped map[string]string) {
	h.t.Helper()

	if len(remapped) == 0 {
		return
	}
	alts := make([]string, 0, len(remapped))
	for old := range remapped {
		alts = append(alts, regexp.QuoteMeta(old))
	}
	// \b keeps ":5432" from matching inside ":54321".
	re := regexp.MustCompile(`:(` + strings.Join(alts, "|") + `)\b`)

	envFile := filepath.Join(h.dir, ".env")
	data, err := os.ReadFile(envFile)
	require.NoError(h.t, err)

	out := re.ReplaceAllStringFunc(string(data), func(m string) string {
		return ":" + remapped[m[1:]]
	})
	require.NoError(h.t, os.WriteFile(envFile, []byte(out), 0o644))
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
	h.runInProject(hamrBin, "gen", "locale")
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
	// package.json lives in frontend/, not the project root.
	h.runInDir(filepath.Join(h.dir, "frontend"), "npm", "install")
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

	// Monitor for early exit so waitHealthy can fail fast. Buffered + closed
	// after the send so BOTH waitHealthy (early-exit path) and Cleanup can
	// receive without one stealing the other's value: whoever reads second gets
	// the zero value from the closed channel instead of blocking forever (which
	// would hang the test until the 10-minute panic, burying the real failure).
	h.exited = make(chan error, 1)
	go func() {
		h.exited <- h.srvCmd.Wait()
		close(h.exited)
	}()

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
	h.runInDir(h.dir, name, args...)
}

func (h *harness) runInDir(dir, name string, args ...string) {
	h.t.Helper()
	cmd := exec.Command(name, args...)
	cmd.Dir = dir
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
				port: freePort(t),
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
				port: freePort(t),
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
