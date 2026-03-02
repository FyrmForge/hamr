# Config — Environment-Based Configuration

`hamr/pkg/config` provides typed accessors for environment variables with sensible
defaults and panic-on-missing semantics. Zero dependencies beyond the standard library.

## Typical Usage

Read config at package level so values are resolved once at startup:

```go
package main

import (
    "log"

    "github.com/FyrmForge/hamr/pkg/config"
    "github.com/FyrmForge/hamr/pkg/logging"

    // To load env from .env
    _ "github.com/joho/godotenv/autoload"
)

var (
    envDBURL   = config.GetEnvOrPanic("DATABASE_URL")
    envPort    = config.GetEnvOrDefaultInt("PORT", 8080)
    envDevMode = config.GetEnvOrDefaultBool("DEV_MODE", false)
)

func main() {
    logger := logging.New(!envDevMode)
    // ...
}
```


## Loading .env Files

Use the `godotenv/autoload` blank import in your `main` package — it loads `.env`
automatically before `main()` runs:

```go
import _ "github.com/joho/godotenv/autoload"
```

## Typed Accessors

Every accessor follows the same pattern: read the env var, parse it, return the default
on failure.

### String

```go
host := config.GetEnvOrDefault("HOST", "localhost")
```

### String (required)

Panics if the variable is unset or empty — use for values that must be present:

```go
dbURL := config.GetEnvOrPanic("DATABASE_URL")
```

### Int

```go
port := config.GetEnvOrDefaultInt("PORT", 8080)
```

Invalid integers fall back to the default silently.

### Bool

```go
debug := config.GetEnvOrDefaultBool("DEBUG", false)
```

Accepts any value `strconv.ParseBool` understands (`true`, `1`, `yes`, etc.).

### Duration

```go
timeout := config.GetEnvOrDefaultDuration("REQUEST_TIMEOUT", 30*time.Second)
```

Accepts any value `time.ParseDuration` understands (`5s`, `100ms`, `1m30s`, etc.).


## API Reference

```go
func GetEnvOrDefault(key, def string) string
func GetEnvOrPanic(key string) string
func GetEnvOrDefaultInt(key string, def int) int
func GetEnvOrDefaultBool(key string, def bool) bool
func GetEnvOrDefaultDuration(key string, def time.Duration) time.Duration
```
