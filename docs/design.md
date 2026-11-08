# Design

Short, living notes on how `health` is put together. Update when a decision changes.

## Package layout

| Path | Owns |
|---|---|
| `cmd/health` | `main`: only wires things up and calls into `internal/cli`. |
| `internal/cli` | Commands, flags, output. Calls `internal/db`; never writes SQL schema. |
| `internal/db` | Database location, opening, migrations. The only package that knows the schema exists as files. |
| `internal/db/migrations` | Numbered SQL files (`0001_init.sql`, ...), embedded into the binary. |

Driver: `modernc.org/sqlite` (pure Go, no cgo), so `go build` works without a C compiler and cross-compiles cleanly.

## Interface for the CLI

```go
path, err := db.DefaultPath() // only if the user passed no --db flag
conn, err := db.Open(path)    // *sql.DB, schema already migrated
defer conn.Close()
```

- `db.Open(path string) (*sql.DB, error)` creates the parent directory (mode 0700), opens the file, sets pragmas (`foreign_keys`, `busy_timeout=5000`, `journal_mode=WAL`), limits the pool to 1 connection, and runs `Migrate`. `":memory:"` works too, which is handy in tests.
- `db.Migrate(conn *sql.DB) error` is exported for tests and tools. `Open` already calls it.
- `db.DefaultPath() (string, error)`, first match wins:
  1. `--db` flag (the CLI checks this itself, before calling `DefaultPath`)
  2. `$HEALTH_DB` (`db.EnvPath`)
  3. `$XDG_DATA_HOME/health/health.db`
  4. macOS: `~/Library/Application Support/health/health.db`
  5. otherwise: `~/.local/share/health/health.db`

## Migrations

- Files are named `NNNN_description.sql`. `schema_migrations(version, applied_at)` records what has run.
- Pending files run in order, each in its own transaction, together with its bookkeeping row.
- Forward-only and idempotent: running it again does nothing. **Never edit a committed migration.** Add a new one instead.

## Schema rationale (migration 0001)

- **Timestamps** are UTC Unix seconds (`INTEGER`). They are unambiguous, cheap to compare, and index well. `tz_offset` (seconds east of UTC) keeps the original local time so reports can show wall-clock times.
- **`samples`** holds every numeric metric in one long table: `metric` (our normalised name, e.g. `heart_rate`), `value`, `unit`. Adding a metric needs no schema change.
- **Dedup.** `UNIQUE(metric, start_ts, end_ts, source)` makes re-imports safe. Use `INSERT ... ON CONFLICT DO NOTHING`. The unique index also serves "metric X between A and B" queries.
- **`source` is `NOT NULL DEFAULT ''`.** SQLite treats NULLs as distinct inside UNIQUE, so a NULL source would let duplicates through.
- **`sleep_stages`** (`stage`, interval) and **`workouts`** (`activity`, `duration_s`, `energy_kcal`, `distance_m`) get their own tables because their shape differs from a single value. Units are fixed (s, kcal, m) so queries never convert.
- **`meta`** is a key/value table for facts about the DB (e.g. `last_import_ts`), not for health data.
- **STRICT tables** reject values of the wrong type instead of storing them silently.
- **Baselines and averages are never stored.** They are always computed from the raw rows, so they can't go stale.

## Data safety

Synthetic data only in this repo. `.gitignore` blocks `*.db` and Apple export files.
