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
	Name            string // project name (when no arg provided)
	Location        string // "subfolder" or "current"
	Owner           string
	CSS             string
	Database        string
	StorageBackend  string // "none" | "local" | "s3"
	S3StaticWatcher string // "yes" | "no"
	WebSocket       string // "yes" | "no"
	E2E             string // "yes" | "no"
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
		Name:            name,
		Location:        defaultLocation,
		CSS:             "plain",
		Database:        "postgres",
		StorageBackend:  "none",
		S3StaticWatcher: "yes",
		WebSocket:       "yes",
		E2E:             "yes",
	}

	// ── Part 1: project setup + stack + storage ──────────────
	var part1 []*huh.Group

	if needsName {
		part1 = append(part1, huh.NewGroup(
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
		))
	}

	if needsLocation && !cmd.Flags().Changed("location") {
		desc := "Subfolder creates ./" + name + ", current directory scaffolds into ."
		if needsName {
			desc = "Subfolder creates ./<name>, current directory scaffolds into ."
		}
		part1 = append(part1, huh.NewGroup(
			huh.NewSelect[string]().
				Title("Project location").
				Description(desc).
				Options(
					huh.NewOption("Use current directory", "current"),
					huh.NewOption("Create subfolder", "subfolder"),
				).
				Value(&res.Location),
		))
	} else if cmd.Flags().Changed("location") {
		res.Location, _ = cmd.Flags().GetString("location")
	}

	if !cmd.Flags().Changed("module") {
		res.Owner = ghUsername()
		part1 = append(part1, huh.NewGroup(
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
		))
	}

	if !cmd.Flags().Changed("css") {
		part1 = append(part1, huh.NewGroup(
			huh.NewSelect[string]().
				Title("CSS approach").
				Options(
					huh.NewOption("Plain CSS — variables, reset, utilities", "plain"),
					huh.NewOption("Tailwind CSS — utility-first with build step", "tailwind"),
				).
				Value(&res.CSS),
		))
	} else {
		res.CSS, _ = cmd.Flags().GetString("css")
	}

	if !cmd.Flags().Changed("database") {
		part1 = append(part1, huh.NewGroup(
			huh.NewSelect[string]().
				Title("Database").
				Options(
					huh.NewOption("PostgreSQL — pgx + sqlx", "postgres"),
				).
				Value(&res.Database),
		))
	} else {
		res.Database, _ = cmd.Flags().GetString("database")
	}

	if !cmd.Flags().Changed("storage") {
		part1 = append(part1, huh.NewGroup(
			huh.NewSelect[string]().
				Title("File storage").
				Options(
					huh.NewOption("None", "none"),
					huh.NewOption("Local folder", "local"),
					huh.NewOption("S3 (MinIO)", "s3"),
				).
				Value(&res.StorageBackend),
		))
	} else {
		res.StorageBackend, _ = cmd.Flags().GetString("storage")
	}

	if len(part1) > 0 {
		if err := huh.NewForm(part1...).Run(); err != nil {
			return nil, err
		}
	}

	// ── S3 static sync (immediately after storage) ───────────
	if res.StorageBackend == "s3" && !cmd.Flags().Changed("s3-watcher") {
		if err := huh.NewForm(huh.NewGroup(
			huh.NewSelect[string]().
				Title("Sync static assets to S3").
				Description("Upload CSS/JS/images to S3 bucket, serve from S3 URL instead of /static").
				Options(
					huh.NewOption("Yes", "yes"),
					huh.NewOption("No", "no"),
				).
				Value(&res.S3StaticWatcher),
		)).Run(); err != nil {
			return nil, err
		}
	}

	// ── Part 2: remaining features ───────────────────────────
	var part2 []*huh.Group

	if !cmd.Flags().Changed("websocket") {
		part2 = append(part2, huh.NewGroup(
			huh.NewSelect[string]().
				Title("WebSocket").
				Description("Real-time connections").
				Options(
					huh.NewOption("Yes", "yes"),
					huh.NewOption("No", "no"),
				).
				Value(&res.WebSocket),
		))
	} else {
		if v, _ := cmd.Flags().GetBool("websocket"); v {
			res.WebSocket = "yes"
		} else {
			res.WebSocket = "no"
		}
	}

	if !cmd.Flags().Changed("e2e") {
		part2 = append(part2, huh.NewGroup(
			huh.NewSelect[string]().
				Title("E2E testing").
				Description("Browser tests with testcontainers").
				Options(
					huh.NewOption("Yes", "yes"),
					huh.NewOption("No", "no"),
				).
				Value(&res.E2E),
		))
	} else {
		if v, _ := cmd.Flags().GetBool("e2e"); v {
			res.E2E = "yes"
		} else {
			res.E2E = "no"
		}
	}

	if len(part2) > 0 {
		if err := huh.NewForm(part2...).Run(); err != nil {
			return nil, err
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
