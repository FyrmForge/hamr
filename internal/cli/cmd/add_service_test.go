package cmd

import (
	"bytes"
	"os"
	"path/filepath"
	"testing"

	"github.com/FyrmForge/hamr/internal/cli/generator"
	"github.com/spf13/cobra"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func newAddServiceTestCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:  "service [name]",
		Args: cobra.MaximumNArgs(1),
		RunE: runAddService,
	}
	cmd.Flags().String("type", "", "")
	cmd.Flags().Bool("db", false, "")
	cmd.Flags().Bool("auth", false, "")
	cmd.Flags().Bool("locale", false, "")
	cmd.Flags().Int("port", 0, "")
	return cmd
}

// writeServiceProject creates a minimal HAMR project root: hamr.toml with
// [options], a go.mod, and env files.
func writeServiceProject(t *testing.T, dir, options string) {
	t.Helper()
	toml := "[hamr]\nversion = \"0.0.0\"\n\n[options]\n" + options +
		"\n[dev]\nproxy_target = \"$PORT\"\n\n[[dev.watch]]\nname = \"site\"\nwatch = \"**/*.go\"\ncmd = \"go build -o ./bin/site ./cmd/site\"\nrun = \"./bin/site\"\n"
	require.NoError(t, os.WriteFile(filepath.Join(dir, "hamr.toml"), []byte(toml), 0o644))
	gomod := "module github.com/test/myproj\n\ngo 1.25.0\n"
	require.NoError(t, os.WriteFile(filepath.Join(dir, "go.mod"), []byte(gomod), 0o644))
	require.NoError(t, os.WriteFile(filepath.Join(dir, ".env"), []byte("PORT=8080\n"), 0o644))
	require.NoError(t, os.WriteFile(filepath.Join(dir, ".env.example"), []byte("PORT=8080\n"), 0o644))
}

func TestAddService_WorkerAllFlags(t *testing.T) {
	dir := t.TempDir()
	writeServiceProject(t, dir, "database = \"postgres\"\ndb_connector = \"sqlx\"\nauth = \"session\"\n")
	chdir(t, dir)

	cmd := newAddServiceTestCmd()
	var buf bytes.Buffer
	cmd.SetOut(&buf)
	cmd.SetArgs([]string{"mailer", "--type", "worker", "--db"})
	require.NoError(t, cmd.Execute())

	for _, rel := range []string{
		"cmd/mailer/main.go",
		"cmd/mailer/Dockerfile",
		"internal/mailer/worker.go",
	} {
		_, err := os.Stat(filepath.Join(dir, rel))
		assert.NoErrorf(t, err, "expected %s to exist", rel)
	}

	mainGo, err := os.ReadFile(filepath.Join(dir, "cmd", "mailer", "main.go"))
	require.NoError(t, err)
	// Options from hamr.toml and module from go.mod flow into the template.
	assert.Contains(t, string(mainGo), "github.com/test/myproj/internal/mailer")
	assert.Contains(t, string(mainGo), "postgres.NewStore")
	// DATABASE_URL default uses the project directory's slug.
	assert.Contains(t, string(mainGo), "localhost:5432/"+generator.ProjectSlug(filepath.Base(dir)))

	assert.Contains(t, buf.String(), `Service "mailer" created`)
}

func TestAddService_APIWithPort(t *testing.T) {
	dir := t.TempDir()
	writeServiceProject(t, dir, "database = \"postgres\"\ndb_connector = \"sqlx\"\nauth = \"none\"\n")
	chdir(t, dir)

	cmd := newAddServiceTestCmd()
	cmd.SetOut(&bytes.Buffer{})
	cmd.SetArgs([]string{"billing", "--type", "api", "--db=false", "--port", "9001"})
	require.NoError(t, cmd.Execute())

	env, err := os.ReadFile(filepath.Join(dir, ".env"))
	require.NoError(t, err)
	assert.Contains(t, string(env), "BILLING_PORT=9001")

	toml, err := os.ReadFile(filepath.Join(dir, "hamr.toml"))
	require.NoError(t, err)
	assert.Contains(t, string(toml), `name = "billing"`)
}

func TestAddService_RequiresHamrToml(t *testing.T) {
	dir := t.TempDir()
	chdir(t, dir)

	cmd := newAddServiceTestCmd()
	cmd.SetOut(&bytes.Buffer{})
	cmd.SetErr(&bytes.Buffer{})
	cmd.SetArgs([]string{"x", "--type", "worker", "--db=false"})
	err := cmd.Execute()
	require.Error(t, err)
	assert.Contains(t, err.Error(), "hamr.toml")
}

func TestAddService_AuthRequiresProjectAuth(t *testing.T) {
	dir := t.TempDir()
	writeServiceProject(t, dir, "database = \"postgres\"\ndb_connector = \"sqlx\"\nauth = \"none\"\n")
	chdir(t, dir)

	cmd := newAddServiceTestCmd()
	cmd.SetOut(&bytes.Buffer{})
	cmd.SetErr(&bytes.Buffer{})
	cmd.SetArgs([]string{"x", "--type", "api", "--db", "--auth", "--port", "9001"})
	err := cmd.Execute()
	require.Error(t, err)
	assert.Contains(t, err.Error(), "session auth")
}

func TestAddService_InvalidHamrTomlErrors(t *testing.T) {
	dir := t.TempDir()
	writeServiceProject(t, dir, "database = \"postgres\"\ndb_connector = \"sqlx\"\nauth = \"none\"\n")
	// A daemon without cmd is a dev-config validation error; the command must
	// refuse to append to a broken config instead of skipping the dup check.
	f, err := os.OpenFile(filepath.Join(dir, "hamr.toml"), os.O_APPEND|os.O_WRONLY, 0o644)
	require.NoError(t, err)
	_, err = f.WriteString("\n[[dev.daemon]]\nname = \"broken\"\n")
	require.NoError(t, err)
	require.NoError(t, f.Close())
	chdir(t, dir)

	cmd := newAddServiceTestCmd()
	cmd.SetOut(&bytes.Buffer{})
	cmd.SetErr(&bytes.Buffer{})
	cmd.SetArgs([]string{"x", "--type", "worker", "--db=false"})
	err = cmd.Execute()
	require.Error(t, err)
	assert.Contains(t, err.Error(), "hamr.toml is invalid")

	_, statErr := os.Stat(filepath.Join(dir, "cmd", "x"))
	assert.True(t, os.IsNotExist(statErr), "nothing should be generated on invalid config")
}

func TestAddService_DuplicateWatchRuleName(t *testing.T) {
	dir := t.TempDir()
	writeServiceProject(t, dir, "database = \"postgres\"\ndb_connector = \"sqlx\"\nauth = \"none\"\n")
	chdir(t, dir)

	cmd := newAddServiceTestCmd()
	cmd.SetOut(&bytes.Buffer{})
	cmd.SetArgs([]string{"w1", "--type", "worker", "--db=false"})
	require.NoError(t, cmd.Execute())

	// Same name again: rejected before touching disk.
	cmd2 := newAddServiceTestCmd()
	cmd2.SetOut(&bytes.Buffer{})
	cmd2.SetErr(&bytes.Buffer{})
	cmd2.SetArgs([]string{"w1", "--type", "worker", "--db=false"})
	err := cmd2.Execute()
	require.Error(t, err)
	assert.Contains(t, err.Error(), "already exists")
}
