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
// paths in .go files under dir. It returns the old module path and the number
// of .go files that were modified.
func RenameModule(dir, newModule string) (oldModule string, filesUpdated int, err error) {
	goModPath := filepath.Join(dir, "go.mod")
	oldModule, _, err = parseGoMod(goModPath)
	if err != nil {
		return "", 0, fmt.Errorf("parse go.mod: %w", err)
	}

	if oldModule == newModule {
		return oldModule, 0, fmt.Errorf("new module path is the same as the current one")
	}

	// Rewrite imports in all .go files.
	err = filepath.WalkDir(dir, func(path string, d os.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		if d.IsDir() || !strings.HasSuffix(path, ".go") {
			return nil
		}

		changed, rewriteErr := rewriteImports(path, oldModule, newModule)
		if rewriteErr != nil {
			return fmt.Errorf("rewrite %s: %w", path, rewriteErr)
		}
		if changed {
			filesUpdated++
		}
		return nil
	})
	if err != nil {
		return oldModule, filesUpdated, err
	}

	// Update go.mod module directive.
	if err := rewriteGoMod(goModPath, oldModule, newModule); err != nil {
		return oldModule, filesUpdated, fmt.Errorf("rewrite go.mod: %w", err)
	}

	return oldModule, filesUpdated, nil
}

// rewriteImports parses a Go file and replaces import paths that match the old
// module prefix with the new one. Returns true if the file was modified.
func rewriteImports(path, oldModule, newModule string) (bool, error) {
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
