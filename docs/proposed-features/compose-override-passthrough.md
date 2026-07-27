# Proposed: `hamr compose` passthrough (+ two scaffold bugs it fixes)

Status: **proposed**

`hamr dev` walks busy compose host ports and records the result in a generated
override file. Anything else that runs `docker compose` against the same
project — the scaffolded Makefile, a user's shell script, an agent — does not
know that file exists, silently merges a *different* config than the one that
is live, and makes compose recreate the stack on the original ports.

Two scaffold bugs fall out of this. Both are reproducible today.

## Background: how the override works

When a host port is busy, `hamr dev` walks it and writes
`.hamr/compose.<entry>.override.yaml` (`internal/devserver/composeports.go`,
`composeOverridePath`). Every compose invocation hamr makes goes through
`composeArgs` (`internal/devserver/devserver.go:1060`), which appends `-f
<override>` when the file exists, on top of `--project-directory <dir> -f
<base>`.

So hamr runs:

```
docker compose --project-directory docker -f docker/docker-compose.yaml \
               -f .hamr/compose.deps.override.yaml up -d
```

…and everyone else runs:

```
docker compose -f docker/docker-compose.yaml up -d
```

Same project name (compose files carry an explicit `name:`), different merged
config. Compose does what it is asked: it reconciles the running containers
back to the un-walked ports.

## Bug 1 — scaffolded `docker-*` targets fight the override

`internal/cli/generator/templates/new/root/Makefile.tmpl:108-121` emits four
single-`-f` targets:

```make
docker-up:      docker compose -f docker/docker-compose.yaml up -d
docker-down:    docker compose -f docker/docker-compose.yaml down
docker-delete:  docker compose -f docker/docker-compose.yaml down -v
docker-reload:  docker-delete docker-up
```

Run `make docker-up` (or anything depending on it) while `hamr dev` is up with
walked ports and compose recreates the containers on the base file's ports.
If those ports are busy — which is *why* they were walked — it hard-fails:

```
Container <proj>-deps-rustfs-1 Creating
Error response from daemon: failed to set up container networking:
  Bind for 0.0.0.0:9000 failed: port is already allocated
make: *** [Makefile:99: docker-up] Error 1
```

The recreate also drops and recreates the volumes, so a developer who was
only trying to restart the stack loses their local data.

This is newly likely to be hit because the dev TUI's `m` palette runs project
Makefile targets from inside `hamr dev` — precisely the context where the
override is live. Observed with `[dev].port_walk` doing
`5432→5433`, `9000→9002`, `9001→9003`.

## Bug 2 — the postgres healthcheck reports ready during initdb

`internal/cli/generator/templates/new/docker/docker-compose.yaml.tmpl:17`:

```yaml
healthcheck:
  test: ["CMD-SHELL", "pg_isready -U postgres"]
```

On a **fresh volume** the postgres entrypoint starts a temporary server to run
`docker-entrypoint-initdb.d/*`, then shuts it down and starts the real one.
That temporary server sets `listen_addresses=''` — it is reachable on the unix
socket only. `pg_isready` with no `-h` uses the socket, so it reports ready
during the bootstrap phase.

Consequences:

- `[[dev.docker_compose]] wait_ready = true` can be satisfied before the real
  server exists, so the app is started against a postgres that is about to
  restart.
- Any script that waits on the same condition and then runs `psql`/`createdb`
  races the shutdown:

  ```
  psql: error: connection to server on socket "..." failed:
    FATAL:  the database system is shutting down
  ```

Fix is one word — probe TCP, which the bootstrap server does not listen on:

```yaml
test: ["CMD-SHELL", "pg_isready -U postgres -h 127.0.0.1"]
```

Verified: with `-h 127.0.0.1`, a fresh-volume bring-up followed immediately by
`createdb` succeeds; without it, it fails as above roughly every time.

## Proposal: `hamr compose [args...]`

Patching the scaffold Makefile fixes Bug 1 for *new* projects and leaves every
hand-written script, CI step and agent to rediscover the same trap. The
durable fix is to expose what `composeArgs` already computes.

```
hamr compose up -d
hamr compose down -v
hamr compose exec -T postgres psql -U postgres
```

`hamr compose` resolves `hamr.toml`, picks the `[[dev.docker_compose]]` entry
(`--name` when there is more than one), assembles
`--project-directory … -f <base> [-f <override>]`, and `exec`s docker compose
with the user's arguments appended. Exit code and streams pass straight
through.

This is the shape `hamr env` already established for the other half of the
same problem: `hamr env --export` exists precisely so `db-sh` picks up walked
DB ports. Walked *host ports for scripts* is solved; walked *compose config
for compose calls* is not. Same gap, same remedy.

### Why not the alternatives

- **Glob the override in the Makefile** — narrowest fix, still per-consumer,
  and hardcodes a hamr-internal path (`.hamr/compose.*.override.yaml`) into
  every scaffold. If that path ever moves, every generated project breaks
  silently. Reasonable as an interim if `hamr compose` is not wanted.
- **Have hamr detect and refuse a conflicting compose invocation** — needs to
  intercept a command hamr does not run. More machinery, less value.
- **Stop walking compose ports** — no. Walking is the right behaviour; only
  the blast radius needs containing.

### Follow-on

With `hamr compose` in place the scaffold Makefile becomes:

```make
docker-up:     hamr compose up -d
docker-down:   hamr compose down
docker-delete: hamr compose down -v
```

…with a documented fallback to raw `docker compose` for environments where the
hamr binary is not on PATH (CI images that only need the stack up, no walking).

## Checklist

- [ ] `hamr compose` subcommand wrapping `composeArgs`
- [ ] Scaffold Makefile `docker-*` targets use it
- [ ] `pg_isready -h 127.0.0.1` in the compose template healthcheck
- [ ] Guide note: why raw `docker compose` can fight a running `hamr dev`
- [ ] `llms.txt` entry for `hamr compose`

## Reproduction

```bash
# occupy the ports hamr will want
docker run -d -p 9000:9000 --name squatter alpine sleep infinity

hamr dev                    # walks 9000→9002, writes .hamr/compose.deps.override.yaml
# in the TUI: press `m`, run docker-up   (or `make docker-up` in another shell)
# → Bind for 0.0.0.0:9000 failed: port is already allocated
```

```bash
# Bug 2, independently
docker compose -f docker/docker-compose.yaml down -v
docker compose -f docker/docker-compose.yaml up -d
docker compose -f docker/docker-compose.yaml exec -T postgres pg_isready -U postgres  # ready
docker compose -f docker/docker-compose.yaml exec -T postgres createdb -U postgres foo
# → FATAL:  the database system is shutting down
```

Found while splitting a scaffolded project into five binaries with four
databases (`stackr-test`), where a `db-setup.sh` helper needed to create the
per-service databases on both fresh and pre-existing volumes.
