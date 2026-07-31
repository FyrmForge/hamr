# Templint — `.templ` File Linter

## CLI

```bash
hamr lint templ [flags]
```

| Flag | Default | Description |
|------|---------|-------------|
| `--rule` | all | Run only these rules (comma-separated IDs) |
| `--config` | `hamr.toml` | Path to hamr.toml config file |
| `--severity` | all | Minimum severity to report: `warning` or `error` |

```bash
hamr lint templ                          # lint current directory (recursive)
hamr lint templ --rule inline-if,img-alt # run only specific rules
hamr lint templ --severity error         # only report errors (skip warnings)
hamr lint templ --config my-hamr.toml    # use a custom config file
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

### htmx / native form actions (severity: error)

| ID | What it catches |
|----|-----------------|
| `no-native-form-actions` | `action=`, `method=`, or `formaction=` attribute on any element. Use `hx-post` / `hx-get` / `hx-put` etc. instead. |
| `htmx-conflict` | Same element has both an `hx-*` attribute and a native form attribute (`action`/`method`/`formaction`). Pick one. |

## Configuration

A `[lint.templ]` section in your `hamr.toml` controls which rules run. Each
entry maps a rule ID to `"warning"`, `"error"`, or `"off"`. **Rules omitted from
the section are disabled.** `"off"` is equivalent to omitting the rule.

Unknown rule IDs and invalid severity strings cause `hamr lint templ` to fail
loudly — typos are surfaced, not silently ignored.

The scaffold generates a `[lint.templ]` block enumerating every rule at its
recommended severity, so a fresh project starts fully linted.

```toml
[lint.templ]
inline-if              = "error"
inline-for             = "error"
inline-switch          = "error"
no-native-form-actions = "error"
htmx-conflict          = "error"
img-alt                = "warning"
no-href                = "warning"
inline-style           = "warning"
empty-class            = "warning"
js-href                = "warning"
```

## Inline Suppression

A `templint:ignore` directive inside a `//` comment silences diagnostics for
one line. On a line of its own it targets the line below; trailing source
content it targets the line it sits on.

```
// templint:ignore <rule-id>[,<rule-id>...] [-- optional reason]
// templint:ignore                          (suppresses every rule)
```

```
// templint:ignore no-native-form-actions -- GitHub's manifest flow requires a browser form POST
<form method="post" action={ templ.SafeURL(action) }>

<img src="logo.png">                    // templint:ignore img-alt
// templint:ignore img-alt, empty-class
<img src="x.png" class="">
```

Everything after ` -- ` is a human reason and is ignored by the parser.

**Multi-line tags.** Rules anchor their diagnostic to the line a tag *opens*
on, so the directive belongs directly above the `<form`, not above the
attribute further down inside it. Only the immediately adjacent line is
covered — there is no cascading lookback, so of two stacked directives only
the nearer one applies.

Two diagnostics keep directives from rotting. Neither is configurable and
neither appears in `[lint.templ]`:

| ID | Severity | When |
|----|----------|------|
| `unknown-rule` | error | The directive names a rule ID that does not exist — a typo cannot silently suppress nothing |
| `unused-suppression` | warning | The directive suppressed no diagnostic on its target line |

A directive naming a rule that is known but switched `"off"` (or absent from
`[lint.templ]`) reports neither — turning a rule off must not turn every
existing suppression into a warning.

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

// Load config (returns nil if file missing or [lint.templ] empty;
// returns an error on unknown rule ID or invalid severity)
cfg, err := templint.LoadConfig("hamr.toml")

// Create linter. nil config = no rules enabled (reports nothing).
// Only rules listed in cfg.Rules run, at the severity configured.
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
