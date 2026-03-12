# Release Process

HAMR uses [go-semantic-release](https://github.com/go-semantic-release/semantic-release) to automate versioning and releases. Pushes to `master` that pass CI trigger the release workflow.

## Commit Prefixes

Version bumps are determined by commit message prefixes following [Conventional Commits](https://www.conventionalcommits.org/):

| Prefix | Bump | Example |
|--------|------|---------|
| `fix:` | patch (0.0.X) | `fix: handle missing hamr.toml gracefully` |
| `feat:` | minor (0.X.0) | `feat: add hamr ai upgrade command` |
| `feat!:` | major (X.0.0) | `feat!: redesign config format` |
| `BREAKING CHANGE:` in body | major (X.0.0) | Any prefix with breaking change footer |

Prefixes that **do not** trigger a release:

- `chore:` — maintenance, dependency updates
- `docs:` — documentation only
- `refactor:` — code changes with no feature or fix
- `test:` — adding or updating tests
- `ci:` — CI/CD changes

## How It Works

1. Push to `master` triggers the CI workflow
2. On CI success, the release workflow runs `go-semantic-release`
3. It analyzes commit messages since the last tag to determine the bump
4. If a bump is needed, it creates a git tag and GitHub release
5. Cross-platform binaries are built with the version baked in via ldflags

## Dev Builds

When running from source without a release tag, `hamr version` shows `dev`. The `hamr new` command resolves the latest git tag and appends `-dev` (e.g. `0.5.0-dev`) when writing scaffold metadata to `hamr.toml`.
