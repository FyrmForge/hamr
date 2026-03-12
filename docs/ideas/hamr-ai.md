# HAMR AI

## Problem

LLM workflows are awkward when the only interface is a normal CLI plus raw source files.

An agent often needs things like:
- A screenshot of the current page
- The visible text from that page
- The HTML / DOM for the same rendered view
- A compact summary of routes, forms, or app structure
- A checked-in text file that simpler agents can read without running tools
- A consistent machine-readable output format instead of ad hoc terminal text

Today, you can piece some of this together with browser tooling, grep, and custom scripts, but there is no single HAMR-native surface for it.

## Proposal

Add a dedicated `hamr ai` namespace for commands whose primary consumer is an LLM, agent, or automation layer.

```text
hamr ai <subcommand>
```

This keeps the intent clear:
- `hamr dev` is for developers
- `hamr sync` is for deployment / assets
- `hamr ai` is for structured context capture and agent-oriented tooling

The first implementation can be small. The important part is defining the namespace and output conventions correctly.

## Design Principles

1. **Machine-first output**
   Default output should be deterministic and easy to parse.

2. **Rendered state beats source alone**
   For UI tasks, a screenshot plus rendered text is more useful than source files by themselves.

3. **Bundle related artifacts**
   A useful AI command often needs multiple outputs from one action:
   screenshot + text + HTML + metadata.

4. **Works locally against a dev app**
   The commands should be useful against `hamr dev`, not just public URLs.

5. **Serve both static-file and tool-using agents**
   Some models can only read repo files, while others can call tools and consume JSON or browser artifacts.

6. **Stable command surface**
   Keep subcommands narrow and composable instead of one giant “do everything” command.

## Proposed CLI Shape

```text
hamr ai
├── capture <url>         Capture screenshot + optional sidecars
├── page <url>            Export a structured page bundle for an LLM
├── routes                Emit app routes in a machine-readable form
├── forms <url>           Extract forms, fields, actions, and buttons from a page
├── context               Generate a compact project context bundle
├── export llms.txt       Generate llms.txt / llms-full.txt style files
├── validate llms.txt     Check generated AI context files for drift / issues
├── upgrade               AI-assisted scaffold upgrade advisor
└── help                  Explain available AI-oriented commands
```

Not all of these need to exist immediately. The namespace matters first.

## Current Status

Status in the current worktree:

- [x] `hamr ai` namespace exists
- [x] `hamr ai capture <url>` is implemented
- [x] Per-capture bundle directories are implemented
- [x] Bundle artifacts include `screenshot.png`, optional `screenshot.txt` / `screenshot.html`, and `meta.json`
- [x] Browser override / detection is implemented
- [x] Viewport sizing via `--width`, `--height`, and `--scale` is implemented
- [x] Full-page capture via `--full-page` is implemented
- [x] Window scroll controls via `--scroll-to`, `--scroll-x`, and `--scroll-y` are implemented
- [x] Scroll-container targeting via `--scroll-selector` is implemented
- [ ] Tiled capture is not implemented yet
- [ ] `hamr ai page` is not implemented yet
- [ ] `hamr ai routes` is not implemented yet
- [ ] `hamr ai forms` is not implemented yet
- [ ] `hamr ai context` is not implemented yet
- [ ] `hamr ai export llms.txt` is not implemented yet
- [ ] `hamr ai validate llms.txt` is not implemented yet
- [x] `hamr ai upgrade` is implemented

## MVP

### 1. `hamr ai capture <url>`

Current status: implemented in the current worktree.

This should be the first command.

Purpose:
- Capture what a browser actually sees
- Save artifacts that an LLM can consume directly

Suggested behavior:
- By default writes a per-capture bundle directory, not a loose file
- Always writes a `.png`
- Optional `.txt` sidecar with visible text
- Optional `.html` sidecar with page HTML
- Optional JSON metadata to stdout or a file

Suggested default output shape:

```text
.hamr/captures/<capture-id>/
  screenshot.png
  screenshot.txt
  screenshot.html
  meta.json
```

Suggested flags:

```text
hamr ai capture <url>
  --dir .hamr/captures
  --out artifacts/login.png
  --text
  --html
  --json
  --selector "#app"
  --full-page
  --width 1440
  --height 1024
  --scroll-to top|middle|bottom
  --scroll-x 0
  --scroll-y 900
  --scroll-selector ".results-pane"
  --wait 1s
  --headless
```

Why it matters:
- Screenshot alone is weak for LLMs
- Text alone loses layout and visual state
- HTML alone misses rendered output

The combination is strong enough to support:
- visual QA
- UI bug triage
- design review
- agent navigation/debugging

#### Capture modes

`hamr ai capture` should support more than one screenshot mode.

Recommended modes:

- **viewport** — the default. Capture exactly what the browser currently shows.
- **full-page** — capture the whole scrollable page as one image.
- **tiled** — capture a series of viewport-sized images from top to bottom.

Why keep all three:

- `viewport` is the most readable and useful for specific UI states
- `full-page` is useful for overall structure and long-form page review
- `tiled` is likely the best long-page format for LLMs, because one extremely tall image often makes text too small

Recommended flag shape:

```text
hamr ai capture <url> --full-page
hamr ai capture <url> --scroll-to bottom
hamr ai capture <url> --tiles
hamr ai capture <url> --tiles --tile-overlap 120
```

The important product point is that `full-page` should be optional, not the default.
For LLM workflows, a tiled bundle plus text/HTML sidecars is likely more useful than one huge PNG.

#### Scroll position

For viewport capture, scroll position should be first-class:

- `--scroll-to top|middle|bottom`
- `--scroll-x <px>`
- `--scroll-y <px>`
- `--scroll-selector <css>` for custom scroll containers

This matters because many modern apps do not use the document body as the only scroll surface.

#### Video / GIFs

Do **not** make video capture part of the first version.

Reason:
- support for MP4/GIF understanding varies a lot across models and clients
- static images are more reliable across agents
- frame bundles plus metadata are usually a better LLM artifact than raw video

If motion capture is ever added later, it should probably look like:
- optional human-facing `recording.mp4`
- extracted frame images
- structured metadata with timestamps, URLs, actions, and scroll positions

That keeps the AI-facing part robust even when video support is weak.

### 2. `hamr ai page <url>`

This is a likely second command.

Purpose:
- Produce one structured bundle instead of several loose files

Example output shape:

```json
{
  "url": "http://localhost:3000/login",
  "title": "Login",
  "screenshot": "artifacts/login.png",
  "text": "artifacts/login.txt",
  "html": "artifacts/login.html",
  "forms": [
    {
      "action": "/login",
      "method": "post",
      "fields": [
        {"name": "email", "type": "email"},
        {"name": "password", "type": "password"}
      ],
      "buttons": ["Sign in"]
    }
  ]
}
```

This is probably a better long-term primitive than raw capture alone, because it gives an agent one coherent artifact.

## Next Wave

### `hamr ai routes`

Purpose:
- Give an LLM a route map without forcing it to infer handlers from source

Possible output:
- method
- path
- handler package/function
- route group / middleware summary

This is useful for:
- navigation planning
- test generation
- feature discovery

### `hamr ai forms <url>`

Purpose:
- Extract form structure from the rendered page

Possible output:
- forms
- field names
- input types
- labels
- required/disabled state
- submit buttons
- HTMX attributes

This is useful for:
- agent-driven browser actions
- form test generation
- accessibility review

### `hamr ai context`

Purpose:
- Generate a compact project bundle for an LLM session

Possible contents:
- module path
- package summary
- route summary
- key config files
- detected frontend stack
- database connector
- available Make targets / scripts

This would be a higher-level replacement for manually feeding a model random files.

## `llms.txt` Integration

`hamr ai` should not replace `llms.txt`. It should own generating and validating it.

Recommended model:
- `llms.txt` remains a checked-in, discoverable entrypoint for agents that only read files
- `hamr ai context` provides fresher and richer structured output for tool-using agents
- `hamr ai export llms.txt` turns the dynamic context into a static artifact

This split works well across different agent types:
- text-only or weaker agents can read `llms.txt`
- coding agents can run `hamr ai context --format json`
- multimodal agents can combine `hamr ai page` or `capture` with `llms.txt` context

### Suggested commands

#### `hamr ai context --format llms-txt`

Purpose:
- Emit a compact `llms.txt`-style summary to stdout

This is useful for:
- piping into another tool
- previewing what would be written
- keeping the file format and the generator aligned

#### `hamr ai export llms.txt`

Purpose:
- Write or refresh `llms.txt` from the current project state

Possible variants:
- `hamr ai export llms.txt`
- `hamr ai export llms-full.txt`
- `hamr ai export --out docs/llms/project.txt --format llms-txt`

#### `hamr ai validate llms.txt`

Purpose:
- Check that generated context files are still useful and not obviously stale

Possible checks:
- referenced files still exist
- generated file is within size budget
- core sections are present
- exported command list matches the actual CLI shape

### Why this should live under `hamr ai`

Because `llms.txt` is really one delivery format for AI context, not a separate product area.

The namespace should cover both:
- dynamic context generation
- static AI-facing file export

That gives HAMR one coherent AI surface instead of:
- one system for browser artifacts
- another system for route/context export
- another system for `llms.txt`

## Output Conventions

This matters as much as the commands themselves.

Recommended rules:

1. Every `hamr ai` command should support `--json`
2. Context-oriented commands should support `--format json|markdown|llms-txt`
3. File-producing commands should return absolute output paths
4. Sidecar files should share a basename
5. Output should avoid noisy prose by default
6. Errors should be structured when `--json` is enabled

Example:

```json
{
  "ok": true,
  "artifacts": {
    "screenshot": "/abs/path/artifacts/login.png",
    "text": "/abs/path/artifacts/login.txt",
    "html": "/abs/path/artifacts/login.html"
  }
}
```

## `hamr ai upgrade`

### Problem

Sites scaffolded from HAMR evolve independently. When HAMR itself improves — new packages, changed patterns, new scaffold options — there is no practical way to automatically upgrade a site that has diverged from the original scaffold. Automatic migrations break on real codebases.

### Approach

`hamr ai upgrade` is a **machine-readable upgrade report**, not an interactive tool. It diffs the scaffold between the version the site was generated from and the current HAMR version, and outputs a structured report with project context, categorized changes, and relevant details.

The command does not apply changes or prompt for input. An LLM agent consumes the output, presents it to the developer conversationally, and handles the back-and-forth about what to adopt.

### How it works

1. Read `hamr.toml` from the project root to get the scaffold version and options
2. Diff the HAMR scaffold templates between the stored version and the current version
3. Categorize the changes and annotate with project context (which options are relevant, which files are affected)
4. Output the full report as structured data

### `hamr.toml`

Every scaffolded site gets a `hamr.toml` that records what was generated and how:

```toml
[hamr]
version = "0.3.2"
scaffolded_at = "2026-03-11"

[options]
database = "postgres"
auth = "session"
css = "tailwind"
websockets = false
mailer = false
queue = false

[added_later]
websockets = true
```

- `[hamr]` tracks the HAMR version the site was scaffolded from
- `[options]` records the original scaffold choices
- `[added_later]` tracks features added post-scaffold via `hamr add`

This gives the AI full context about the project's starting point and current shape.

### Change categories

Changes are classified into categories:

- **structural** — route organization, directory layout, file naming conventions
- **package_update** — API changes in existing HAMR packages
- **new_package** — new HAMR packages or libraries added to the scaffold
- **new_option** — scaffold options that didn't exist at the original version
- **pattern** — middleware, handler, or template patterns that changed
- **config** — config format or default changes

### Example usage

A developer asks their LLM agent:

> "Run hamr ai upgrade and let's see what's changed since we scaffolded"

The agent runs `hamr ai upgrade --json` and gets back structured data it can reason about. It then presents the changes conversationally:

> "You're on HAMR v0.3.2, current is v0.5.0. Here's what changed:
>
> 1. **Route registration** — HAMR moved to group-based route registration in v0.4.0. Your project uses the flat pattern. Want me to walk through what the migration would look like?
>
> 2. **Validation package v2** — The API changed in v0.4.2 and you use it in 3 files. Want me to update the usage?
>
> 3. **Rate limiting** — New middleware added in v0.5.0. You don't have rate limiting yet. Interested?
>
> 4. Mailer got improvements too, but you don't use mailer so I'll skip that."

The agent handles the interaction. The command just provides the data.

### Example JSON output

```json
{
  "project": {
    "scaffolded_version": "0.3.2",
    "current_version": "0.5.0",
    "scaffolded_at": "2026-03-11",
    "options": {
      "database": "postgres",
      "auth": "session",
      "css": "tailwind",
      "websockets": false,
      "mailer": false
    },
    "added_later": ["websockets"]
  },
  "changes": [
    {
      "category": "structural",
      "title": "Group-based route registration",
      "since": "0.4.0",
      "relevant": true,
      "summary": "Route registration moved from flat to group-based pattern",
      "affected_scaffold_files": ["internal/routes/routes.go", "internal/routes/auth.go"],
      "diff": "..."
    },
    {
      "category": "package_update",
      "title": "Validation package v2",
      "since": "0.4.2",
      "relevant": true,
      "summary": "hamr/validate API changed: Validate() now returns structured errors",
      "affected_scaffold_files": ["internal/handlers/auth.go"],
      "diff": "..."
    },
    {
      "category": "new_package",
      "title": "Rate limiting middleware",
      "since": "0.5.0",
      "relevant": true,
      "summary": "Built-in rate limiting middleware added to scaffold",
      "affected_scaffold_files": ["internal/middleware/ratelimit.go"],
      "diff": "..."
    },
    {
      "category": "package_update",
      "title": "Mailer improvements",
      "since": "0.4.5",
      "relevant": false,
      "relevance_reason": "mailer not in options or added_later",
      "summary": "Mailer package gained template support",
      "affected_scaffold_files": ["internal/mailer/mailer.go"],
      "diff": "..."
    }
  ]
}
```

### Flags

```text
hamr ai upgrade
  --json             Output as structured JSON (default for machine consumption)
  --category <cat>   Filter to a specific category
  --from <version>   Override the version to diff from (instead of reading hamr.toml)
  --relevant-only    Only show changes relevant to the project's options
```

### Open questions

- Should each change include the full scaffold diff, or just a summary with pointers?
- How much context per change — just the scaffold diff, or also changelog notes?
- Should there be a `--format markdown` for agents that prefer reading a file over parsing JSON?

## Config

`hamr.toml` serves double duty: scaffold metadata for upgrades, and AI command configuration.

```toml
[hamr]
version = "0.3.2"
scaffolded_at = "2026-03-11"

[options]
database = "postgres"
auth = "session"
css = "tailwind"
websockets = false

[added_later]
websockets = true

[ai]
artifacts_dir = ".hamr/ai"
base_url = "http://localhost:3000"
headless = true
width = 1440
height = 1024
wait = "1s"
```

This would let `hamr ai capture /login` resolve against the local dev app automatically.

## What Probably Does *Not* Belong Here

These are tempting, but likely too broad for the first version:
- free-form code summarization commands
- chat-style commands that just wrap an LLM API
- vague “analyze my app” commands with unstable output

`hamr ai` should provide concrete artifacts and structured context, not become a general chatbot shell.

## Recommendation

Start with:
1. `[x]` `hamr ai capture <url>`
2. `[ ]` `hamr ai page <url>`
3. `[ ]` `hamr ai routes`
4. `[ ]` `hamr ai context --format llms-txt`
5. `[ ]` `hamr ai export llms.txt`
6. `[ ]` `hamr ai upgrade`

That gives us:
- rendered UI capture
- structured page context
- application structure discovery
- a static file handoff for agents that cannot run tools
- AI-assisted scaffold upgrades via `hamr.toml` metadata

That set is still narrow, but now it covers dynamic AI workflows, static AI workflows, and project lifecycle management.

## Open Questions

- Should `hamr ai capture` also support local file paths / `file://` URLs?
- Should `hamr ai page` include accessibility data like headings, landmarks, and labels?
- Should route extraction rely on runtime registration, static analysis, or both?
- Should the default artifact directory live under `.hamr/ai/`?
- Should `hamr ai` commands prefer stdout JSON by default, or human text unless `--json` is set?
- Should `hamr ai upgrade` generate actual code patches, or just describe what to change?
- Should dismissed upgrade suggestions be persisted so they don't reappear?
- How should `hamr ai upgrade` handle suggestion dependencies (e.g., suggestion 2 requires suggestion 1)?
- Should `hamr.toml` be generated at scaffold time, or backfill-able for existing projects?
