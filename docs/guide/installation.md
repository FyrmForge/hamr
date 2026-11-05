# Installation

HAMR is a Go module plus a `hamr` CLI that scaffolds projects and runs the dev server. You need Go, the CLI, and the templ code generator.

## Requirements

- **Go 1.27 or later.**
- **[templ](https://templ.guide)** on your `PATH`. `hamr dev` and `make build` run `templ generate`, and the CLI version must match the `github.com/a-h/templ` version in your project's `go.mod`.
- **Docker**, only for PostgreSQL projects or S3 storage: `hamr dev` starts the database and RustFS with Docker Compose. SQLite projects with local storage need no containers.
- **Node.js and npm**, only if you pick Tailwind CSS. Plain CSS projects never touch Node.
- **ffmpeg** (with `ffprobe`), only if you use `pkg/media` for image or video processing.

## Install the CLI

```sh
go install github.com/FyrmForge/hamr/cmd/hamr@latest
hamr version
```

`go install` puts the binary in `$(go env GOPATH)/bin`; make sure that directory is on your `PATH`. `hamr version` prints `hamr <version> (<commit>)`.

Inside a scaffolded project, `make install` installs `hamr` and `templ` at the versions pinned in its `go.mod` (plus `npm install` for Tailwind projects), so every developer and CI run uses the same tools.

## Shell completion

```sh
hamr completion install
```

This detects your shell from `$SHELL` and writes a per-user completion script for bash, zsh or fish. Pass `--shell zsh` to override detection or `--system` for a system-wide install. For zsh per-user installs, `~/.zsh/completions` must be on your `fpath`; see [CLI reference → `hamr completion`](cli.md#hamr-completion) for the paths and the `~/.zshrc` lines.

## Using the packages without the CLI

The framework packages under `pkg/` work in any Go project:

```sh
go get github.com/FyrmForge/hamr@latest
```

```go
import (
    "github.com/FyrmForge/hamr/pkg/config"
    "github.com/FyrmForge/hamr/pkg/logging"
    "github.com/FyrmForge/hamr/pkg/server"
)
```

Next, [create a project](create-project.md).
