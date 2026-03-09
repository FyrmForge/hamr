# Templint — `.templ` File Linter

## CLI

```bash
hamr lint templ [flags]
```

| Flag | Default | Description |
|------|---------|-------------|
| `--rule` | all | Run only these rules (comma-separated IDs) |
| `--config` | `.templint.yml` | Path to config file |
| `--severity` | all | Minimum severity to report: `warning` or `error` |

```bash
hamr lint templ                          # lint current directory (recursive)
hamr lint templ --rule inline-if,img-alt # run only specific rules
hamr lint templ --severity error         # only report errors (skip warnings)
hamr lint templ --config .templint.yml   # use a custom config file
```

**Exit codes:** `0` if no error-severity diagnostics, `1` if any errors found. Warnings alone do not cause a non-zero exit.

**Output format** (one line per finding, grep-friendly):

```
internal/web/handler/home/home.templ:12:3: error[inline-if] inline if with HTML body is silently dropped by templ
static/css/layout.templ:8:5: warning[img-alt] <img> tag missing alt attribute
```

---

`pkg/templint` is a static linter for `.templ` files. It catches patterns that templ silently ignores (producing no output and no error), accessibility gaps, and style issues.

## Rules

### Control Flow (severity: error)

These catch patterns that templ silently drops — no output and no compile error.

| ID | What it catches |
|----|-----------------|
| `inline-if` | `if cond { <div>...</div> }` on a single line |
| `inline-for` | `for _, x := range items { <li>...</li> }` on a single line |
| `inline-switch` | `switch x { case "a": <span>...</span> }` on a single line |

**Fix:** always put the HTML body on a separate line:

```
// Bad — silently dropped
if user != nil { <span>{ user.Name }</span> }

// Good
if user != nil {
    <span>{ user.Name }</span>
}
```

### Accessibility (severity: warning)

| ID | What it catches |
|----|-----------------|
| `img-alt` | `<img>` tags missing `alt` attribute |
| `no-href` | `<a>` tags missing `href` attribute |

### Style (severity: warning)

| ID | What it catches |
|----|-----------------|
| `inline-style` | `style="..."` or `style={ ... }` attributes |
| `empty-class` | `class=""` empty class attributes |
| `js-href` | `href="javascript:..."` links |

## Configuration

Create a `.templint.yml` in your project root (optional — all rules are enabled by default):

```yaml
rules:
  inline-if:
    enabled: true
    severity: error
  inline-style:
    enabled: false
  img-alt:
    severity: error
```

Each rule accepts:
- `enabled` — `true` (default) or `false`
- `severity` — `"warning"` or `"error"`

## Makefile Integration

Generated projects include a `templint` target:

```makefile
templint:
	hamr lint templ
```

## CI Integration

The generated CI workflow includes a templ lint step:

```yaml
- name: Lint templ files
  run: go run github.com/FyrmForge/hamr/cmd/hamr@latest lint templ
```

## Library Usage

```go
import "github.com/FyrmForge/hamr/pkg/templint"

// Load config (returns nil if file not found — uses defaults)
cfg, err := templint.LoadConfig(".templint.yml")

// Create linter (nil config = all rules enabled)
linter := templint.New(cfg)

// Lint a directory
diags, err := linter.LintDir(".")

// Or lint a single file
diags, err := linter.LintFile("home.templ")

// Filter and check results
diags = templint.FilterBySeverity(diags, templint.Error)
if templint.HasErrors(diags) {
    for _, d := range diags {
        fmt.Println(d) // home.templ:12:3: error[inline-if] ...
    }
}
```
