# ADR-001: SEO Packages — `pkg/seo` and `pkg/sitemap`

- **Status**: Proposed
- **Date**: 2026-03-30
- **Authors**: JamesTiberiusKirk

## Context

HAMR-generated projects currently have zero SEO infrastructure — no sitemap, no Open Graph
tags, no canonical URLs, no structured data, no robots.txt. The stoxtrakr app (built on hamr)
recently implemented comprehensive SEO support at the application level. The patterns used
are generic and should be extracted into framework packages so every `hamr new` project gets
production-ready SEO out of the box.

OG image generation (Chromium/Rod-based screenshots) is explicitly **out of scope** — it's
app-specific and pulls in a heavy browser dependency. The `PageMeta.OGImage` field exists so
apps can wire their own generation pipeline.

## Decisions

### Always-on, not feature-flagged

SEO meta tags in the layout are zero-cost when no `PageMeta` is set — they fall back to
sensible defaults or emit nothing. There is no heavy dependency, no background process, no
required config. Sitemap and robots.txt routes are always registered in the scaffold. Every
web app benefits from having these, and removing them is easier than adding them.

No `IncludeSEO` generator flag.

### Two packages, not one

`pkg/seo` handles per-page metadata (description, canonical, OG, JSON-LD, noindex).
`pkg/sitemap` handles sitemap.xml and robots.txt generation. They have no dependency on each
other. A project that only needs meta tags doesn't import sitemap, and vice versa.

### Context-based metadata injection (`pkg/seo`)

Handlers call `seo.SetMeta(c, seo.PageMeta{...})` to store per-page metadata in Echo's
context. The layout template reads it back via accessor methods with layered fallback:
page-specific value > app-level default > hardcoded safe default. This avoids changing
the `Layout` component signature and works transparently with existing handlers that don't
set any metadata.

**`PageMeta` struct fields:**
`Title`, `Description`, `Canonical` (path only), `OGType`, `OGImage`, `OGImageAlt`,
`JSONLD` (raw JSON string), `NoIndex` (bool), `Extra` (map[string]string).

Uses `pkg/ctx` typed keys for storage — consistent with how middleware stores flash messages,
locale, and subject data.

### NoIndex via middleware, not hardcoded paths

`seo.NoIndexPrefixes(prefixes ...string)` returns an `echo.MiddlewareFunc` that sets
`NoIndex = true` for matching request paths. The scaffold wires it with sensible defaults
(`/login`, `/register`, `/account`, `/dashboard`, `/admin`). Apps can customise the list.
The seo package itself has no hardcoded path knowledge.

### Builder pattern for sitemap (`pkg/sitemap`)

```go
sm := sitemap.New(baseURL).
    Add(sitemap.Entry{Loc: "/", ChangeFreq: "daily", Priority: 1.0}).
    AddFunc(func() []Entry { /* query DB */ })
```

Static entries via `Add()`, dynamic entries via `AddFunc()` callbacks invoked at request time.
`Handler()` returns an `echo.HandlerFunc` for `/sitemap.xml`. `RobotsHandler(cfg)` returns
one for `/robots.txt`. Both registered outside session/CSRF middleware in the scaffold.

### Site-level defaults in the scaffold, not the package

`SiteName`, `DefaultDescription`, `DefaultOGImage` live as package-level vars in the
generated `components/helpers.go` — consistent with existing `BaseURL`, `StaticBaseURL`,
`StaticVersion`. Thin wrapper functions (`SEODescription(c)`, `SEOCanonical(c)`, etc.) bind
these defaults to the `seo` package accessors so the layout template stays clean.

### JSON-LD: WebSite schema by default

`seo.DefaultJSONLD(siteName, siteURL, description)` builds a `schema.org/WebSite` JSON-LD
object. Handlers can override with page-specific schemas (e.g., `WebPage`, `Product`,
`Article`) via `PageMeta.JSONLD`. The layout emits whichever is set via `<script
type="application/ld+json">`.

## Packages

### `pkg/seo`

| Export | Purpose |
|--------|---------|
| `PageMeta` struct | Per-page SEO metadata |
| `SetMeta(c, meta)` | Store metadata in Echo context |
| `GetMeta(c) PageMeta` | Retrieve metadata (zero-value if absent) |
| `PageMeta.GetDescription(fallback)` | Description with fallback |
| `PageMeta.GetOGType()` | OG type, defaults to "website" |
| `PageMeta.GetCanonical(c, baseURL)` | Canonical URL, falls back to request path |
| `PageMeta.GetOGImage(fallback)` | OG image URL with fallback |
| `PageMeta.GetJSONLD(fallback)` | JSON-LD with fallback |
| `DefaultJSONLD(name, url, desc)` | WebSite JSON-LD builder |
| `NoIndexPrefixes(prefixes...)` | Middleware that sets NoIndex for matching paths |

Dependencies: `echo/v4`, `hamr/pkg/ctx`, `encoding/json`.

### `pkg/sitemap`

| Export | Purpose |
|--------|---------|
| `Entry` struct | Sitemap URL entry (Loc, LastMod, ChangeFreq, Priority) |
| `New(baseURL) *Sitemap` | Constructor |
| `Sitemap.Add(entries...) *Sitemap` | Register static entries (chainable) |
| `Sitemap.AddFunc(fn) *Sitemap` | Register dynamic entry callback (chainable) |
| `Sitemap.Handler() echo.HandlerFunc` | Serve sitemap.xml |
| `RobotsConfig` struct | SitemapURL, Disallows |
| `RobotsHandler(cfg) echo.HandlerFunc` | Serve robots.txt |

Dependencies: `echo/v4`, `encoding/xml`, `strconv`. No hamr imports.

## Scaffold Changes

- **`layout.templ.tmpl`** — Add to `<head>`: description, canonical, robots noindex
  (conditional), OG tags, Twitter Card tags, JSON-LD script.
- **`helpers.go.tmpl`** — Add `SiteName`, `DefaultDescription`, `DefaultOGImage` vars and
  `SEO*()` wrapper functions. Always import `echo/v4` and `hamr/pkg/seo`.
- **`server.go.tmpl`** — Register `/sitemap.xml` and `/robots.txt` routes outside
  session/CSRF group. Add `seo.NoIndexPrefixes(...)` to the site group (conditional on
  `IncludeAuth`).
- **Handler templates** (`home`, `about`) — Add `seo.SetMeta()` calls with page descriptions.

## Alternatives Considered

### Single `pkg/seo` package containing sitemap

Rejected. Sitemap generation is orthogonal to per-page metadata. Combining them would mean
projects that only need meta tags also import XML encoding. Separate packages follow hamr's
existing pattern of focused, single-responsibility packages.

### Feature-flagged via `IncludeSEO`

Rejected. The SEO tags are purely additive and zero-cost. Forcing users to opt in means most
projects ship without basic SEO. The tags render harmlessly with default values when no
handler sets metadata.

### Store metadata in a custom header or separate context map

Rejected. Using `pkg/ctx` typed keys is the established hamr pattern for per-request data
(subject, flash, locale, translator). Adding another mechanism would be inconsistent.

## Documentation

- New: `docs/guide/pkg/seo.md`, `docs/guide/pkg/sitemap.md`
- Update: `docs/guide/pkg/README.md` (add entries)
- Update: `llmsdocs/llms.txt` and `llmsdocs/llms-full.txt` (add package sections)
