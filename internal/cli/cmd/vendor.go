package cmd

import (
	"fmt"

	"github.com/FyrmForge/hamr/internal/cli/generator"
	"github.com/spf13/cobra"
)

var vendorCmd = &cobra.Command{
	Use:   "vendor [dep[@version]]",
	Short: "Download and checksum frontend JS dependencies",
	Long: `Vendor frontend JavaScript dependencies (htmx, alpine, idiomorph) from CDN.

Downloads files to static/js/ and records checksums in hamr.vendor.json.

Examples:
  hamr vendor                          # vendor all deps at default/locked versions
  hamr vendor htmx                     # vendor only htmx
  hamr vendor alpine@3.14.9            # vendor alpine at pinned version
  hamr vendor --update                 # re-vendor all at latest
  hamr vendor --verify                 # check checksums
  hamr vendor --url <url> --out <path> # custom dep`,
	Args: cobra.MaximumNArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		verify, _ := cmd.Flags().GetBool("verify")
		update, _ := cmd.Flags().GetBool("update")
		url, _ := cmd.Flags().GetString("url")
		out, _ := cmd.Flags().GetString("out")

		dir := "."

		if verify {
			if err := generator.VendorVerify(dir); err != nil {
				return err
			}
			fmt.Println("All checksums verified.")
			return nil
		}

		if url != "" {
			if out == "" {
				return fmt.Errorf("--out is required when using --url")
			}
			if err := generator.VendorCustom(dir, url, out); err != nil {
				return err
			}
			fmt.Printf("Vendored %s → %s\n", url, out)
			return nil
		}

		if len(args) == 1 {
			name := args[0]
			if err := generator.VendorOne(dir, name, update); err != nil {
				return err
			}
			fmt.Printf("Vendored %s\n", name)
			return nil
		}

		if err := generator.VendorAll(dir, update); err != nil {
			return err
		}
		fmt.Println("All dependencies vendored.")
		return nil
	},
}

func init() {
	vendorCmd.Flags().Bool("update", false, "re-download all dependencies at latest versions")
	vendorCmd.Flags().Bool("verify", false, "verify checksums of vendored files")
	vendorCmd.Flags().String("url", "", "custom URL to download")
	vendorCmd.Flags().String("out", "", "output path for custom URL (relative to project root)")
}
