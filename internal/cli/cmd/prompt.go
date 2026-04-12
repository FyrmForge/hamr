package cmd

import (
	"fmt"
	"os/exec"
	"regexp"
	"strings"

	"github.com/charmbracelet/huh"
	"github.com/spf13/cobra"
)

// wizardResult holds the raw values collected by the interactive form.
type wizardResult struct {
	Name             string // project name (when no arg provided)
	Location         string // "subfolder" or "current"
	Owner            string
	CSS              string
	Database         string
	DBConnector      string // "sqlx" | "gorm"
	MigrateAtStartup string // "yes" | "no"
	StorageBackend   string // "local" | "s3"
	StaticS3         string // "yes" | "no"
	PgAdmin          string // "yes" | "no"
	Locale           string // "yes" | "no"
	WebSocket        string // "yes" | "no"
	E2E              string // "yes" | "no"
	Stripe           string // "yes" | "no"
}

// wizardStep pairs a huh group with a callback that prints the selection.
type wizardStep struct {
	group *huh.Group
	print func()
}

// runInteractiveForm builds and runs a huh form for any options that weren't
// explicitly set via flags. Returns the collected values.
//
// needsName indicates no positional arg was given (user must name the project).
// needsLocation indicates the wizard should ask subfolder vs current directory.
func runInteractiveForm(cmd *cobra.Command, name string, needsName, needsLocation bool) (*wizardResult, error) {
	defaultLocation := "current"
	if !needsName && name != "" {
		defaultLocation = "subfolder"
	}
	res := &wizardResult{
		Name:             name,
		Location:         defaultLocation,
		CSS:              "plain",
		Database:         "postgres",
		DBConnector:      "sqlx",
		MigrateAtStartup: "no",
		StorageBackend:   "local",
		StaticS3:         "yes",
		PgAdmin:          "yes",
		Locale:           "yes",
		WebSocket:        "yes",
		E2E:              "yes",
		Stripe:           "yes",
	}

	var steps []wizardStep

	// ── Project name ────────────────────────────────────────
	if needsName {
		steps = append(steps, wizardStep{
			group: huh.NewGroup(
				huh.NewInput().
					Title("Project name").
					Placeholder("myapp").
					Value(&res.Name).
					Validate(func(s string) error {
						if s == "" {
							return fmt.Errorf("required")
						}
						if !regexp.MustCompile(`^[a-zA-Z][a-zA-Z0-9_-]*$`).MatchString(s) {
							return fmt.Errorf("must start with a letter and contain only letters, digits, hyphens, or underscores")
						}
						return nil
					}),
			),
			print: func() {
				fmt.Printf("  Project name: %s\n", res.Name)
			},
		})
	}

	// ── Project location ────────────────────────────────────
	if needsLocation && !cmd.Flags().Changed("location") {
		desc := "Subfolder creates ./" + name + ", current directory scaffolds into ."
		if needsName {
			desc = "Subfolder creates ./<name>, current directory scaffolds into ."
		}
		steps = append(steps, wizardStep{
			group: huh.NewGroup(
				huh.NewSelect[string]().
					Title("Project location").
					Description(desc).
					Options(
						huh.NewOption("Use current directory", "current"),
						huh.NewOption("Create subfolder", "subfolder"),
					).
					Value(&res.Location),
			),
			print: func() {
				if res.Location == "current" {
					fmt.Println("  Location: current directory")
				} else {
					n := res.Name
					if n == "" {
						n = name
					}
					fmt.Printf("  Location: subfolder ./%s\n", n)
				}
			},
		})
	} else if cmd.Flags().Changed("location") {
		res.Location, _ = cmd.Flags().GetString("location")
	}

	// ── GitHub username / module ─────────────────────────────
	if !cmd.Flags().Changed("module") {
		res.Owner = ghUsername()
		steps = append(steps, wizardStep{
			group: huh.NewGroup(
				huh.NewInput().
					Title("GitHub username or org").
					DescriptionFunc(func() string {
						n := res.Name
						if n == "" {
							n = name
						}
						return fmt.Sprintf("Module will be github.com/<owner>/%s", n)
					}, &res.Name).
					Placeholder("username").
					Value(&res.Owner).
					Validate(func(s string) error {
						if s == "" {
							return fmt.Errorf("required")
						}
						return nil
					}),
			),
			print: func() {
				n := res.Name
				if n == "" {
					n = name
				}
				fmt.Printf("  Module: github.com/%s/%s\n", res.Owner, n)
			},
		})
	}

	// ── CSS approach ────────────────────────────────────────
	if !cmd.Flags().Changed("css") {
		steps = append(steps, wizardStep{
			group: huh.NewGroup(
				huh.NewSelect[string]().
					Title("CSS approach").
					Description("Controls how your project's stylesheets are organized and built.").
					Options(
						huh.NewOption("Plain CSS — organized variables, reset, and utility classes with no build step", "plain"),
						huh.NewOption("Tailwind CSS — utility-first framework requiring Node.js and a CSS build step", "tailwind"),
					).
					Value(&res.CSS),
			),
			print: func() {
				switch res.CSS {
				case "tailwind":
					fmt.Println("  CSS: Tailwind CSS")
				default:
					fmt.Println("  CSS: Plain CSS")
				}
			},
		})
	} else {
		res.CSS, _ = cmd.Flags().GetString("css")
	}

	// ── Database ────────────────────────────────────────────
	if !cmd.Flags().Changed("database") {
		steps = append(steps, wizardStep{
			group: huh.NewGroup(
				huh.NewSelect[string]().
					Title("Database").
					Description("Sets up a connection pool and Docker Compose service for local development.").
					Options(
						huh.NewOption("PostgreSQL", "postgres"),
					).
					Value(&res.Database),
			),
			print: func() {
				fmt.Println("  Database: PostgreSQL")
			},
		})
	} else {
		res.Database, _ = cmd.Flags().GetString("database")
	}

	// ── DB connector ───────────────────────────────────────
	if !cmd.Flags().Changed("db-connector") {
		steps = append(steps, wizardStep{
			group: huh.NewGroup(
				huh.NewSelect[string]().
					Title("DB connector").
					Description("Choose how your application talks to the database.").
					Options(
						huh.NewOption("sqlx — raw SQL with pgx", "sqlx"),
						huh.NewOption("GORM — ORM with struct-based models", "gorm"),
					).
					Value(&res.DBConnector),
			),
			print: func() {
				switch res.DBConnector {
				case "gorm":
					fmt.Println("  DB connector: GORM")
				default:
					fmt.Println("  DB connector: sqlx")
				}
			},
		})
	} else {
		res.DBConnector, _ = cmd.Flags().GetString("db-connector")
	}

	// ── Migrate at startup ─────────────────────────────────
	if !cmd.Flags().Changed("migrate-startup") {
		steps = append(steps, wizardStep{
			group: huh.NewGroup(
				huh.NewSelect[string]().
					Title("Migrate at startup").
					Description("Run migrations automatically when the server starts, or use a separate command.").
					Options(
						huh.NewOption("No — run via separate command", "no"),
						huh.NewOption("Yes — run migrations when server starts", "yes"),
					).
					Value(&res.MigrateAtStartup),
			),
			print: func() {
				if res.MigrateAtStartup == "yes" {
					fmt.Println("  Migrate at startup: Yes")
				} else {
					fmt.Println("  Migrate at startup: No")
				}
			},
		})
	} else {
		if v, _ := cmd.Flags().GetBool("migrate-startup"); v {
			res.MigrateAtStartup = "yes"
		} else {
			res.MigrateAtStartup = "no"
		}
	}

	// ── File storage ────────────────────────────────────────
	if !cmd.Flags().Changed("storage") {
		steps = append(steps, wizardStep{
			group: huh.NewGroup(
				huh.NewSelect[string]().
					Title("File storage").
					Description("Adds a storage layer for handling user-uploaded files like avatars or documents.").
					Options(
						huh.NewOption("Local", "local"),
						huh.NewOption("Local + S3 (with RustFS for local dev)", "s3"),
					).
					Value(&res.StorageBackend),
			),
			print: func() {
				switch res.StorageBackend {
				case "s3":
					fmt.Println("  Storage: Local + S3 (RustFS)")
				default:
					fmt.Println("  Storage: Local")
				}
			},
		})
	} else {
		res.StorageBackend, _ = cmd.Flags().GetString("storage")
	}

	// Run each step individually so we can print after each answer.
	// We run the steps collected so far before the static-s3 question,
	// since we need to know the storage backend value.
	for _, s := range steps {
		if err := huh.NewForm(s.group).Run(); err != nil {
			return nil, err
		}
		s.print()
	}

	// ── Static asset syncing (S3 only) ─────────────────────
	if res.StorageBackend == "s3" && !cmd.Flags().Changed("static-s3") {
		if err := huh.NewForm(huh.NewGroup(
			huh.NewSelect[string]().
				Title("Static asset syncing").
				Description("Adds a background daemon to hamr dev that syncs your static/ directory to a separate S3 bucket, and a Makefile target for CI deployments.").
				Options(
					huh.NewOption("Yes", "yes"),
					huh.NewOption("No", "no"),
				).
				Value(&res.StaticS3),
		)).Run(); err != nil {
			return nil, err
		}
		if res.StaticS3 == "yes" {
			fmt.Println("  Static S3 sync: Yes")
		} else {
			fmt.Println("  Static S3 sync: No")
		}
	} else if cmd.Flags().Changed("static-s3") {
		if v, _ := cmd.Flags().GetBool("static-s3"); v {
			res.StaticS3 = "yes"
		} else {
			res.StaticS3 = "no"
		}
	}

	// ── pgAdmin (Postgres only) ────────────────────────────
	if res.Database == "postgres" && !cmd.Flags().Changed("pgadmin") {
		if err := huh.NewForm(huh.NewGroup(
			huh.NewSelect[string]().
				Title("pgAdmin").
				Description("Adds a pgAdmin web UI container to Docker Compose for managing your local Postgres database.").
				Options(
					huh.NewOption("Yes", "yes"),
					huh.NewOption("No", "no"),
				).
				Value(&res.PgAdmin),
		)).Run(); err != nil {
			return nil, err
		}
		if res.PgAdmin == "yes" {
			fmt.Println("  pgAdmin: Yes")
		} else {
			fmt.Println("  pgAdmin: No")
		}
	} else if cmd.Flags().Changed("pgadmin") {
		if v, _ := cmd.Flags().GetBool("pgadmin"); v {
			res.PgAdmin = "yes"
		} else {
			res.PgAdmin = "no"
		}
	}

	// ── Localisation (i18n) ────────────────────────────────
	if !cmd.Flags().Changed("locale") {
		if err := huh.NewForm(huh.NewGroup(
			huh.NewSelect[string]().
				Title("Localisation (i18n)").
				Description("Adds JSON translation files, generated type-safe Go accessors, plural rules, and locale middleware for multi-language support.").
				Options(
					huh.NewOption("Yes", "yes"),
					huh.NewOption("No", "no"),
				).
				Value(&res.Locale),
		)).Run(); err != nil {
			return nil, err
		}
		if res.Locale == "yes" {
			fmt.Println("  Locale (i18n): Yes")
		} else {
			fmt.Println("  Locale (i18n): No")
		}
	} else {
		if v, _ := cmd.Flags().GetBool("locale"); v {
			res.Locale = "yes"
		} else {
			res.Locale = "no"
		}
	}

	// ── WebSocket ───────────────────────────────────────────
	if !cmd.Flags().Changed("websocket") {
		if err := huh.NewForm(huh.NewGroup(
			huh.NewSelect[string]().
				Title("WebSocket").
				Description("Includes a client-side JS helper and server wiring for persistent two-way connections, useful for live updates or chat features.").
				Options(
					huh.NewOption("Yes", "yes"),
					huh.NewOption("No", "no"),
				).
				Value(&res.WebSocket),
		)).Run(); err != nil {
			return nil, err
		}
		if res.WebSocket == "yes" {
			fmt.Println("  WebSocket: Yes")
		} else {
			fmt.Println("  WebSocket: No")
		}
	} else {
		if v, _ := cmd.Flags().GetBool("websocket"); v {
			res.WebSocket = "yes"
		} else {
			res.WebSocket = "no"
		}
	}

	// ── E2E testing ─────────────────────────────────────────
	if !cmd.Flags().Changed("e2e") {
		if err := huh.NewForm(huh.NewGroup(
			huh.NewSelect[string]().
				Title("E2E testing").
				Description("Scaffolds a Go-based browser test suite using testcontainers to spin up Postgres and your app in Docker, with helpers for form submission and assertions.").
				Options(
					huh.NewOption("Yes", "yes"),
					huh.NewOption("No", "no"),
				).
				Value(&res.E2E),
		)).Run(); err != nil {
			return nil, err
		}
		if res.E2E == "yes" {
			fmt.Println("  E2E tests: Yes")
		} else {
			fmt.Println("  E2E tests: No")
		}
	} else {
		if v, _ := cmd.Flags().GetBool("e2e"); v {
			res.E2E = "yes"
		} else {
			res.E2E = "no"
		}
	}

	// ── Payment provider ───────────────────────────────────
	if !cmd.Flags().Changed("stripe") {
		if err := huh.NewForm(huh.NewGroup(
			huh.NewSelect[string]().
				Title("Payment provider").
				Description("Adds a webhook handler with signature verification and an event-type dispatch map.").
				Options(
					huh.NewOption("Stripe — webhook handler using the stripe-go SDK", "yes"),
					huh.NewOption("None", "no"),
				).
				Value(&res.Stripe),
		)).Run(); err != nil {
			return nil, err
		}
		if res.Stripe == "yes" {
			fmt.Println("  Payment provider: Stripe")
		} else {
			fmt.Println("  Payment provider: None")
		}
	} else {
		if v, _ := cmd.Flags().GetBool("stripe"); v {
			res.Stripe = "yes"
		} else {
			res.Stripe = "no"
		}
	}

	return res, nil
}

// ghUsername returns the authenticated GitHub username via the gh CLI.
// Returns "" if gh is not installed or not authenticated.
func ghUsername() string {
	out, err := exec.Command("gh", "api", "user", "--jq", ".login").Output()
	if err != nil {
		return ""
	}
	return strings.TrimSpace(string(out))
}
