# Duplication Review

Date: 2026-05-06

## Scope

This note captures a focused duplication review of the committed `hamr` repo. The goal is not to remove every repeated line, but to identify the duplicated code that is most likely to create maintenance drift, bugs, or unnecessary size growth.

## Summary

The duplication problem in this repo is real, but concentrated. The main pattern is feature-matrix duplication:

- the same logic repeated across image and video media stores
- the same migration runner structure repeated across PostgreSQL and SQLite packages
- the same generated repository logic repeated across backend variants
- the same HTTP handler glue repeated throughout the devserver
- the same connection/backoff behavior repeated between runtime packages and generated templates

This matters less because of raw line count and more because changes to one behavior often need to be propagated across several copies.

## Highest-Signal Findings

### 1. Media store serving/signing logic is duplicated and already drifting

The strongest duplication hotspot is the parallel implementation of store behavior in:

- `pkg/media/image_store.go`
- `pkg/media/video_store.go`

Duplicated areas include:

- local vs S3 URL base selection
- signed URL generation
- local handler path stripping
- S3 handler path stripping
- content-type selection by extension
- response cache headers
- `ServeHandler()` dispatch logic

Relevant locations:

- `pkg/media/image_store.go:273`
- `pkg/media/image_store.go:289`
- `pkg/media/image_store.go:326`
- `pkg/media/image_store.go:333`
- `pkg/media/image_store.go:372`
- `pkg/media/video_store.go:277`
- `pkg/media/video_store.go:291`
- `pkg/media/video_store.go:319`
- `pkg/media/video_store.go:326`
- `pkg/media/video_store.go:362`

This duplication is not harmless. It has already drifted into behavior bugs:

- the S3 serving path mismatch exists in duplicated codepaths rather than one shared implementation
- image/video path and MIME handling are maintained independently even when the control flow is nearly identical

Recommendation:

- introduce shared internal helpers for:
  - URL base resolution
  - signed URL closure wiring
  - local path extraction
  - proxied object serving with configurable MIME mapping
- keep image/video-specific path naming separate, but collapse the serving mechanics

### 2. Migration runners are near copies across database packages

The migration APIs in:

- `pkg/db/migrate.go`
- `pkg/db/sqlite/migrate.go`

are structurally almost identical.

Repeated functions:

- `Migrate`
- `MigrateDown`
- `MigrateSteps`
- `MigrateVersion`
- `MigrateForce`
- `newMigrate`

Relevant locations:

- `pkg/db/migrate.go:22-112`
- `pkg/db/sqlite/migrate.go:21-106`

The only meaningful differences are:

- the driver implementation used
- error prefixes
- the exported config shape

Risk:

- fixes or semantics can diverge between packages
- API parity becomes manual work
- public behavior can drift silently

There is already evidence that this area is not tightly controlled: `pkg/db/migrate.go` exposes a `Driver` field while still hard-wiring the postgres migration driver internally.

Recommendation:

- factor shared migration flow into a smaller private abstraction
- or deliberately split the packages harder so the public API no longer pretends to be parallel when the implementation is not

### 3. Generated repo templates duplicate the same CRUD logic across backend variants

The generator template matrix multiplies repo code across:

- postgres vs sqlite
- sqlx vs gorm

Example:

- `internal/cli/generator/templates/new/internal/repo/postgres/users.go.tmpl`
- `internal/cli/generator/templates/new/internal/repo/sqlite/users.go.tmpl`

Relevant locations:

- `.../postgres/users.go.tmpl:17-83`
- `.../sqlite/users.go.tmpl:17-83`

Most of the SQLX branch differs only by placeholder style:

- `$1` vs `?`

Most of the GORM branch is effectively identical.

Risk:

- every repo/auth behavior change has to be ported through four template variants
- tests have to cover a wider generator matrix
- bugs in generated apps are more likely to be fixed in one branch and missed in another

Recommendation:

- reduce the template matrix where possible
- prefer shared partials or parameterized snippets where only SQL placeholder syntax changes
- keep backend divergence only where behavior truly differs

### 4. Connection retry/backoff logic is duplicated between runtime code and generated code

The retry/jitter connection pattern appears both in:

- `pkg/db/db.go`
- `internal/cli/generator/templates/new/internal/db/gorm-db.go.tmpl`

Relevant locations:

- `pkg/db/db.go:85-206`
- `internal/cli/generator/templates/new/internal/db/gorm-db.go.tmpl:29-107`

Repeated behavior includes:

- `Connect` delegating to `ConnectContext`
- retry loop structure
- timeout-wrapped ping
- exponential backoff
- jitter function
- cancellation handling

This is cross-layer duplication rather than same-package duplication, but it still matters. Runtime fixes to retry behavior or observability do not automatically propagate into generated applications.

Recommendation:

- decide whether generated projects should intentionally own their DB bootstrap code
- if not, move more of this behavior behind reusable runtime packages instead of emitting copies

### 5. Devserver HTTP boilerplate is repeated heavily

`internal/devserver` contains repeated handler scaffolding, especially:

- method guards
- same-origin checks
- HTML response headers and write flow

Examples:

- `internal/devserver/mailmock.go:367-374`
- `internal/devserver/mailmock.go:422-429`
- `internal/devserver/mailmock.go:432-439`
- `internal/devserver/stripemock_dashboard.go:71-78`
- `internal/devserver/stripemock_page.go`
- `internal/devserver/mailmock_page.go`

There is also repeated HTML page write logic across several page handlers:

- set `Content-Type`
- set `Cache-Control: no-store`
- write the buffer

Risk:

- `internal/devserver` is already the largest subsystem in the repo
- controller-layer duplication increases size quickly
- simple security or behavior changes require many edits

Recommendation:

- add small helpers for:
  - `requireMethod`
  - same-origin POST guard
  - standard HTML response write
- keep handlers explicit, but reduce repeated glue

## Secondary Findings

### Test duplication is significant

There is substantial repeated setup/assert scaffolding in tests, especially in:

- `internal/cli/generator/project_test.go`
- `pkg/media/media_test.go`
- `internal/devserver/proxy_test.go`

Examples:

- repeated `buildProjectFileList` + `dests` setup in `internal/cli/generator/project_test.go`
- repeated local image/video store setup in `pkg/media/media_test.go`
- repeated proxy server setup and GET assertions in `internal/devserver/proxy_test.go`

This is lower severity than runtime duplication, but it increases file size and makes tests harder to evolve.

### Documentation duplication also exists

Some config snippets and examples are repeated across docs files, especially in dev workflow docs. This is not the main risk area, but it contributes to maintenance weight and drift potential.

## Prioritized Refactor Targets

If the goal is to reduce maintenance cost without destabilizing the repo, the best order is:

1. `pkg/media`
2. `pkg/db` migration packages
3. generator repo template matrix
4. `internal/devserver` handler boilerplate
5. large repeated test scaffolding

## Practical Interpretation

The repo does not have a uniform duplication problem. It has a concentrated duplication problem in the exact places where feature combinations multiply code:

- storage backends
- database backends
- generated vs runtime implementations
- UI handler variants

That means broad cleanup is less important than targeted consolidation around those axes.

## Suggested Next Step

If this review is used for follow-up work, the best next artifact would be a short refactor plan with:

- one section per hotspot
- intended consolidation boundary
- expected payoff
- risk level
- whether the refactor should happen before or after new feature work
