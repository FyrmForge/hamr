package cmd

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"

	"github.com/FyrmForge/hamr/internal/cli/generator"
	"github.com/FyrmForge/hamr/internal/devserver"
	"github.com/FyrmForge/hamr/internal/scaffold"
	"github.com/charmbracelet/huh"
	"github.com/spf13/cobra"
)

var addServiceCmd = &cobra.Command{
	Use:   "service [name]",
	Short: "Add a new Go binary (service) to a HAMR project",
	Long: `Add a new Go binary to an existing HAMR project.

Creates cmd/<name>/ (main.go + Dockerfile) and internal/<name>/, appends a
[[dev.watch]] rule to hamr.toml so hamr dev builds and runs it, and for HTTP
types appends a <NAME>_PORT entry to .env and .env.example.

Service types:
  empty    Bare binary — env/logging bootstrap and a Run() stub
  worker   Background worker — signal handling, graceful shutdown, optional DB
  api      JSON API server — pkg/server on its own port, optional DB/auth
  html     HTML web server — pkg/server + templ, reuses the project layout

Must be run from the root of a HAMR project (a directory containing hamr.toml).
Any option not provided as a flag is asked interactively.

Examples:
  hamr add service
  hamr add service billing --type api --db --port 8081
  hamr add service mailer --type worker --db=false`,
	Args: cobra.MaximumNArgs(1),
	RunE: runAddService,
}

func init() {
	addServiceCmd.Flags().String("type", "", "service type: empty, worker, api, or html")
	addServiceCmd.Flags().Bool("db", false, "wire the project's database store")
	addServiceCmd.Flags().Bool("auth", false, "wire session auth middleware (api/html; requires --db and a project with auth)")
	addServiceCmd.Flags().Bool("locale", false, "wire the locale bundle (html; requires a project with locale)")
	addServiceCmd.Flags().Int("port", 0, "listen port for api/html services (default 8081)")
	addCmd.AddCommand(addServiceCmd)
}

func runAddService(cmd *cobra.Command, args []string) error {
	cwd, err := os.Getwd()
	if err != nil {
		return fmt.Errorf("get working directory: %w", err)
	}

	meta, err := scaffold.LoadMetadata(filepath.Join(cwd, "hamr.toml"))
	if err != nil {
		return fmt.Errorf("no hamr.toml in current directory — run from a HAMR project root")
	}

	module, goVersion, err := generator.ReadExistingGoMod(cwd)
	if err != nil {
		return fmt.Errorf("read go.mod: %w", err)
	}
	if module == "" {
		return fmt.Errorf("no go.mod in current directory — run from a HAMR project root")
	}

	cfg := &generator.ServiceConfig{
		Module:        module,
		GoVersion:     goVersion,
		ProjectSlug:   generator.ProjectSlug(filepath.Base(cwd)),
		Database:      meta.Options.Database,
		DBConnector:   meta.Options.DBConnector,
		DefaultLocale: meta.Locale.Default,
	}
	if len(args) == 1 {
		cfg.Name = args[0]
	}

	// Load the dev config up front: a broken hamr.toml must fail the command
	// (not silently skip the duplicate-name check), and appending to a broken
	// config would only bury the original error.
	devCfg, err := devserver.LoadConfig(filepath.Join(cwd, "hamr.toml"))
	if err != nil {
		return fmt.Errorf("hamr.toml is invalid — fix it before adding a service: %w", err)
	}
	for _, w := range devCfg.Dev.Watch {
		if w.Name == "templ" {
			cfg.HasTemplRule = true
			break
		}
	}

	authAvailable := meta.Options.Auth == "session"
	localeAvailable := meta.Options.Locale

	if err := runServiceWizard(cmd, cwd, cfg, devCfg, authAvailable, localeAvailable); err != nil {
		return err
	}

	if err := validateServiceName(cwd, cfg.Name, devCfg); err != nil {
		return err
	}
	if cfg.WithAuth && !authAvailable {
		return fmt.Errorf("--auth requires a project scaffolded with session auth")
	}
	if cfg.WithLocale && !localeAvailable {
		return fmt.Errorf("--locale requires a project scaffolded with locale support")
	}
	if err := cfg.Validate(); err != nil {
		return err
	}

	if err := generator.GenerateService(cwd, cfg); err != nil {
		return err
	}

	out := cmd.OutOrStdout()
	_, _ = fmt.Fprintf(out, "\nService %q created:\n", cfg.Name)
	_, _ = fmt.Fprintf(out, "  cmd/%s/            main.go + Dockerfile\n", cfg.Name)
	_, _ = fmt.Fprintf(out, "  internal/%s/       service code\n", cfg.Name)
	_, _ = fmt.Fprintf(out, "  hamr.toml          [[dev.watch]] rule appended\n")
	if cfg.HTTP() {
		_, _ = fmt.Fprintf(out, "  .env               %s_PORT=%d appended\n", cfg.EnvPrefix(), cfg.Port)
	}
	_, _ = fmt.Fprintln(out, "\nNext steps:")
	_, _ = fmt.Fprintln(out, "  hamr dev")
	if cfg.HTTP() {
		_, _ = fmt.Fprintf(out, "  http://localhost:%d\n", cfg.Port)
	}
	return nil
}

// validateServiceName checks the name format and that nothing on disk or in
// hamr.toml already claims it.
func validateServiceName(dir, name string, devCfg *devserver.Config) error {
	if err := generator.ValidateProjectName(name); err != nil {
		return fmt.Errorf("service name: %w", err)
	}
	for _, d := range []string{filepath.Join("cmd", name), filepath.Join("internal", name)} {
		if _, err := os.Stat(filepath.Join(dir, d)); err == nil {
			return fmt.Errorf("%s already exists", d)
		}
	}
	// Watch-rule names must be unique for hamr dev's dependency graph.
	for _, w := range devCfg.Dev.Watch {
		if w.Name == name {
			return fmt.Errorf("hamr.toml already has a [[dev.watch]] rule named %q", name)
		}
	}
	for _, d := range devCfg.Dev.Daemons {
		if d.Name == name {
			return fmt.Errorf("hamr.toml already has a [[dev.daemon]] named %q", name)
		}
	}
	return nil
}

// runServiceWizard fills cfg from flags, prompting interactively for anything
// not provided. Mirrors the hamr new wizard: each answered step is printed.
func runServiceWizard(cmd *cobra.Command, cwd string, cfg *generator.ServiceConfig, devCfg *devserver.Config, authAvailable, localeAvailable bool) error {
	// ── Name ───────────────────────────────────────────────
	if cfg.Name == "" {
		if err := huh.NewForm(huh.NewGroup(
			huh.NewInput().
				Title("Service name").
				Description("Becomes cmd/<name>, internal/<name>, and bin/<name>.").
				Placeholder("worker").
				Value(&cfg.Name).
				Validate(func(s string) error {
					if s == "" {
						return fmt.Errorf("required")
					}
					return validateServiceName(cwd, s, devCfg)
				}),
		)).Run(); err != nil {
			return err
		}
		fmt.Printf("  Service name: %s\n", cfg.Name)
	}

	// ── Type ───────────────────────────────────────────────
	if cmd.Flags().Changed("type") {
		cfg.Type, _ = cmd.Flags().GetString("type")
	} else {
		if err := huh.NewForm(huh.NewGroup(
			huh.NewSelect[string]().
				Title("Service type").
				Options(
					huh.NewOption("Worker — background loop with graceful shutdown", "worker"),
					huh.NewOption("API web server — JSON routes on its own port", "api"),
					huh.NewOption("HTML web server — templ pages on its own port", "html"),
					huh.NewOption("Empty — bare binary with a Run() stub", "empty"),
				).
				Value(&cfg.Type),
		)).Run(); err != nil {
			return err
		}
		fmt.Printf("  Type: %s\n", cfg.Type)
	}

	// ── Database ───────────────────────────────────────────
	if cfg.Type != "empty" {
		if cmd.Flags().Changed("db") {
			cfg.WithDB, _ = cmd.Flags().GetBool("db")
		} else {
			answer := "yes"
			if err := huh.NewForm(huh.NewGroup(
				huh.NewSelect[string]().
					Title("Database").
					Description("Wire the project's repo store into the service.").
					Options(
						huh.NewOption("Yes", "yes"),
						huh.NewOption("No", "no"),
					).
					Value(&answer),
			)).Run(); err != nil {
				return err
			}
			cfg.WithDB = answer == "yes"
			fmt.Printf("  Database: %s\n", answer)
		}
	}

	// ── Auth ───────────────────────────────────────────────
	// Sessions live in the store, so auth needs both an auth-enabled project
	// and the DB wired in.
	if (cfg.Type == "api" || cfg.Type == "html") && authAvailable && cfg.WithDB {
		if cmd.Flags().Changed("auth") {
			cfg.WithAuth, _ = cmd.Flags().GetBool("auth")
		} else {
			answer := "no"
			if err := huh.NewForm(huh.NewGroup(
				huh.NewSelect[string]().
					Title("Session auth").
					Description("Wire session middleware against the shared store (the site keeps owning login).").
					Options(
						huh.NewOption("No", "no"),
						huh.NewOption("Yes", "yes"),
					).
					Value(&answer),
			)).Run(); err != nil {
				return err
			}
			cfg.WithAuth = answer == "yes"
			fmt.Printf("  Session auth: %s\n", answer)
		}
	} else if cmd.Flags().Changed("auth") {
		cfg.WithAuth, _ = cmd.Flags().GetBool("auth")
	}

	// ── Locale ─────────────────────────────────────────────
	if cfg.Type == "html" && localeAvailable {
		if cmd.Flags().Changed("locale") {
			cfg.WithLocale, _ = cmd.Flags().GetBool("locale")
		} else {
			answer := "no"
			if err := huh.NewForm(huh.NewGroup(
				huh.NewSelect[string]().
					Title("Localisation (i18n)").
					Description("Load the project's locale bundle and enable the locale middleware.").
					Options(
						huh.NewOption("No", "no"),
						huh.NewOption("Yes", "yes"),
					).
					Value(&answer),
			)).Run(); err != nil {
				return err
			}
			cfg.WithLocale = answer == "yes"
			fmt.Printf("  Locale: %s\n", answer)
		}
	} else if cmd.Flags().Changed("locale") {
		cfg.WithLocale, _ = cmd.Flags().GetBool("locale")
	}

	// ── Port ───────────────────────────────────────────────
	if cfg.Type == "api" || cfg.Type == "html" {
		if cmd.Flags().Changed("port") {
			cfg.Port, _ = cmd.Flags().GetInt("port")
		} else {
			portStr := "8081"
			if err := huh.NewForm(huh.NewGroup(
				huh.NewInput().
					Title("Listen port").
					Description("The site uses PORT (8080); pick a different one.").
					Value(&portStr).
					Validate(func(s string) error {
						n, err := strconv.Atoi(strings.TrimSpace(s))
						if err != nil || n < 1 || n > 65535 {
							return errors.New("must be a port number (1-65535)")
						}
						return nil
					}),
			)).Run(); err != nil {
				return err
			}
			cfg.Port, _ = strconv.Atoi(strings.TrimSpace(portStr))
			fmt.Printf("  Port: %d\n", cfg.Port)
		}
	}

	return nil
}
