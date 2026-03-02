# Config — Environment-Based Configuration

`hamr/pkg/config` provides typed accessors for environment variables with sensible
defaults and `.env` file loading via godotenv. Zero dependencies beyond the standard
library and godotenv.

## Quick Start

```go
import "github.com/FyrmForge/hamr/pkg/config"
```

## Loading .env Files

```go
if err := config.LoadEnvFile(); err != nil {
    log.Fatal(err)
}
```

Defaults to `.env` in the working directory. Pass explicit paths to load other files:

```go
config.LoadEnvFile(".env", ".env.local")
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

## Typical Usage

```go
func main() {
    _ = config.LoadEnvFile()

    dbURL   := config.GetEnvOrPanic("DATABASE_URL")
    port    := config.GetEnvOrDefaultInt("PORT", 8080)
    devMode := config.GetEnvOrDefaultBool("DEV_MODE", false)

    logger := logging.New(!devMode)
    // ...
}
```

## API Reference

```go
func LoadEnvFile(paths ...string) error
func GetEnvOrDefault(key, def string) string
func GetEnvOrPanic(key string) string
func GetEnvOrDefaultInt(key string, def int) int
func GetEnvOrDefaultBool(key string, def bool) bool
func GetEnvOrDefaultDuration(key string, def time.Duration) time.Duration
```
