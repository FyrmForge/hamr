package generator

import (
	"bytes"
	"fmt"
	"go/ast"
	"go/parser"
	"go/printer"
	"go/token"
	"os"
	"path/filepath"
	"strings"
)

// RenameModule rewrites the module directive in go.mod and updates all import
// paths in .go files under dir. When dryRun is true it detects changes and
// prints affected files but does not write anything. It returns the old module
// path and the number of .go files that were (or would be) modified.
func RenameModule(dir, newModule string, dryRun bool) (oldModule string, filesUpdated int, err error) {
	goModPath := filepath.Join(dir, "go.mod")
	oldModule, _, err = parseGoMod(goModPath)
	if err != nil {
		return "", 0, fmt.Errorf("parse go.mod: %w", err)
	}

	if oldModule == newModule {
		return oldModule, 0, fmt.Errorf("new module path is the same as the current one")
	}

	// Rewrite imports in all .go and .templ files.
	err = filepath.WalkDir(dir, func(path string, d os.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		if d.IsDir() {
			// Skip directories that should never be rewritten: VCS metadata,
			// third-party/vendored code, and Go test fixtures (the go toolchain
			// ignores testdata/ for builds, and it routinely holds intentionally
			// malformed .go files that would abort the walk on parse). The root
			// dir is never skipped even if it happens to share one of these names.
			if path != dir {
				switch d.Name() {
				case ".git", "vendor", "node_modules", "testdata":
					return filepath.SkipDir
				}
			}
			return nil
		}

		// Never write through a symlink: a .go/.templ entry that is actually a
		// symlink could point outside the project (e.g. ~/.ssh/authorized_keys),
		// and os.WriteFile would clobber the target.
		if d.Type()&os.ModeSymlink != 0 {
			return nil
		}

		switch {
		case strings.HasSuffix(path, ".go"):
			changed, rewriteErr := rewriteImports(path, oldModule, newModule, dryRun)
			if rewriteErr != nil {
				return fmt.Errorf("rewrite %s: %w", path, rewriteErr)
			}
			if changed {
				if dryRun {
					fmt.Printf("  would update: %s\n", path)
				}
				filesUpdated++
			}
		case strings.HasSuffix(path, ".templ"):
			changed, rewriteErr := rewriteTemplImports(path, oldModule, newModule, dryRun)
			if rewriteErr != nil {
				return fmt.Errorf("rewrite %s: %w", path, rewriteErr)
			}
			if changed {
				if dryRun {
					fmt.Printf("  would update: %s\n", path)
				}
				filesUpdated++
			}
		}
		return nil
	})
	if err != nil {
		return oldModule, filesUpdated, err
	}

	// Update go.mod module directive.
	if !dryRun {
		if err := rewriteGoMod(goModPath, oldModule, newModule); err != nil {
			return oldModule, filesUpdated, fmt.Errorf("rewrite go.mod: %w", err)
		}
	}

	return oldModule, filesUpdated, nil
}

// rewriteImports parses a Go file and replaces import paths that match the old
// module prefix with the new one. Returns true if the file was modified.
// When dryRun is true, it detects changes but does not write the file.
func rewriteImports(path, oldModule, newModule string, dryRun bool) (bool, error) {
	fset := token.NewFileSet()
	f, err := parser.ParseFile(fset, path, nil, parser.ParseComments)
	if err != nil {
		return false, err
	}

	changed := false
	for _, imp := range f.Imports {
		impPath := strings.Trim(imp.Path.Value, `"`)
		if impPath == oldModule || strings.HasPrefix(impPath, oldModule+"/") {
			newPath := newModule + impPath[len(oldModule):]
			imp.Path.Value = `"` + newPath + `"`
			changed = true
		}
	}

	if !changed {
		return false, nil
	}

	if dryRun {
		return true, nil
	}

	// Rewrite any package-qualified identifiers that use named imports aren't
	// affected, but we need to update the AST so the printer outputs correctly.
	// The import spec values are already updated, so just print.
	return true, writeAST(fset, f, path)
}

// writeAST writes the AST back to the file, preserving the original file mode.
func writeAST(fset *token.FileSet, f *ast.File, path string) error {
	var buf bytes.Buffer
	if err := printer.Fprint(&buf, fset, f); err != nil {
		return err
	}

	info, err := os.Stat(path)
	if err != nil {
		return err
	}

	return os.WriteFile(path, buf.Bytes(), info.Mode())
}

// rewriteTemplImports rewrites import paths in .templ files, which can't be
// parsed by go/parser. The rewrite is scoped to import lines only — a naive
// whole-file replace would also rewrite the module path where it legitimately
// appears in markup (a string literal, an href, displayed text). Templ imports
// use Go syntax, so they're either inside an `import ( … )` block or on a single
// `import "…"` line, all near the top of the file. When dryRun is true it
// detects changes without writing.
func rewriteTemplImports(path, oldModule, newModule string, dryRun bool) (bool, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return false, err
	}

	lines := strings.Split(string(data), "\n")
	changed := false
	inImportBlock := false
	for i, line := range lines {
		trimmed := strings.TrimSpace(line)
		isImportLine := false
		switch {
		case inImportBlock:
			isImportLine = true
			// Exit on the block's closing paren, whether it's on its own line
			// (`)`) or trailing the last import (`"…/pkg")`). Matching only a
			// bare ")" would leave the block "open" for the unformatted-but-valid
			// trailing-paren case and rewrite the rest of the file.
			if strings.HasSuffix(trimmed, ")") {
				inImportBlock = false
			}
		case strings.HasPrefix(trimmed, "import ("):
			isImportLine = true // covers a one-liner `import ("x")` too
			inImportBlock = !strings.HasSuffix(trimmed, ")")
		case strings.HasPrefix(trimmed, "import "):
			isImportLine = true
		}
		if !isImportLine {
			continue
		}
		repl := strings.ReplaceAll(line, `"`+oldModule+`"`, `"`+newModule+`"`)
		repl = strings.ReplaceAll(repl, `"`+oldModule+"/", `"`+newModule+"/")
		if repl != line {
			lines[i] = repl
			changed = true
		}
	}

	if !changed {
		return false, nil
	}

	if dryRun {
		return true, nil
	}

	info, err := os.Stat(path)
	if err != nil {
		return false, err
	}
	return true, os.WriteFile(path, []byte(strings.Join(lines, "\n")), info.Mode())
}

// rewriteGoMod replaces the module directive line in go.mod.
func rewriteGoMod(path, oldModule, newModule string) error {
	data, err := os.ReadFile(path)
	if err != nil {
		return err
	}

	oldDirective := "module " + oldModule
	newDirective := "module " + newModule
	updated := strings.Replace(string(data), oldDirective, newDirective, 1)

	if updated == string(data) {
		return fmt.Errorf("module directive %q not found in %s", oldDirective, path)
	}

	return os.WriteFile(path, []byte(updated), 0o644)
}
