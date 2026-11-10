# Design

Short, living notes on how `health` is put together. Update when a decision changes.

## Package layout

| Path | Owns |
|---|---|
| `cmd/healthcli` | `main`: only wires things up and calls into `internal/cli`. |
| `internal/cli` | Commands, flags, output. Calls `internal/db`; never writes SQL schema. |
| `internal/db` | Database location, opening, migrations. The only package that knows the schema exists as files. |
| `internal/db/migrations` | Numbered SQL files (`0001_init.sql`, ...), embedded into the binary. |
| `internal/stats` | Read side: daily values, baselines, sleep nights, workouts. Read-only SQL. |

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
  2. `$HEALTHCLI_DB` (`db.EnvPath`)
  3. `$XDG_DATA_HOME/healthcli/healthcli.db`
  4. macOS: `~/Library/Application Support/healthcli/healthcli.db`
  5. otherwise: `~/.local/share/healthcli/healthcli.db`

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

## Read side (`internal/stats`)

Built for `today` and `trend` (`today` now goes through `stats.BuildDay`, see below). Days are `"YYYY-MM-DD"` strings, ranges are inclusive, days with no data are left out (the caller fills gaps).

```go
stats.DailySeries(ctx, conn, metric, fromDay, toDay string) ([]stats.DayValue, error) // {Day, Value, Source, Samples}
stats.BaselineFor(ctx, conn, metric, day string, days int) (stats.Baseline, error)   // {Metric, From, To, Mean, Stddev, N}
stats.Compare(value float64, b stats.Baseline) stats.Comparison                     // {Value, Mean, Delta, DeltaPct, Z, Label}
stats.SleepNights(ctx, conn, fromDay, toDay string) ([]stats.SleepNight, error)
stats.LatestSample(ctx, conn, metric string) (stats.Sample, error)                  // stats.ErrNoData if none
stats.Workouts(ctx, conn, fromDay, toDay string) ([]stats.Workout, error)
stats.AddDays(day string, n int) (string, error); stats.SourceClass(name) string
```

`metric` accepts names and aliases (`steps`, `hr`, `weight`).

**Rules**

- **Local day** = date of `start_ts + tz_offset`: the date on the clock where the reading was taken, so a trip from Rome to New York needs no special case. A sample that spans midnight counts for the day it starts. SQL first narrows by a UTC window padded by the widest real offsets (-12h/+14h, so the indexes are used), then keeps rows whose local date is in range.
- **One source per metric per day.** Sources are classed by name, case-insensitive substring: `watch` > `iphone` > `manual` (what `healthcli log` writes) > anything else. Only the best class with data that day is used; the rest are ignored, never added. Two sources in the same class (old and new iPhone) are not added either: more rows that day wins, then the name, so results never depend on row order.
- **Daily aggregation** lives in the metrics registry (`Metric.Daily`):
  - `sum`: `step_count`, `active_energy` (amounts that add up over the day).
  - `avg`: `heart_rate`, `hrv_sdnn` (rates sampled many times), and `resting_heart_rate`: Apple writes about one per day but may write a revised estimate; the mean is stable and equals the value on normal days.
  - `latest`: `body_mass`. Averaging a morning and an evening weigh-in gives a number nobody saw on the scale; the newest reading wins.
- **Baseline** = mean and sample stddev (n-1) of the daily values of the `days` days *before* `day` (default 28, four of every weekday). The target day is excluded so today is compared with the past, not itself. Fewer than 7 days with data returns the partial Baseline (`N` set) plus `ErrNotEnoughData`.
- **Compare**: `Z = (value - mean) / stddev`. Label `above` if `Z >= 1`, `below` if `Z <= -1`, else `typical` (one standard deviation: about two thirds of ordinary days are "typical"). Stddev 0 (identical days): any difference is above/below, `Z` is 0. `DeltaPct` is 0 when the mean is 0. Float noise (1e-9 relative) is snapped to 0.
- **Sleep**: `Asleep = core + deep + rem + asleep_unspecified`; `awake` and `in_bed` are reported but are not sleep. A night is named after the local date it ends; a sleep day runs 18:00 to 18:00, because the Watch's first stage row of a night (e.g. 23:12-23:59) ends before midnight and would otherwise split the night in two. Stage totals come from the one best source with stage rows that night; `in_bed` is picked separately (same rule), because the iPhone usually writes in-bed while the Watch writes stages.
- **Workouts** apply no per-day rule (a session is not a running total) but drop a duplicate recorded by another source; **LatestSample** applies none either:  "latest" means the newest reading. LatestSample only uses priority to break a tie at the exact same timestamp.

**Indexes (migration 0002).** Samples need nothing new: the `UNIQUE(metric, start_ts, ...)` index serves the range query. `sleep_stages(end_ts)` and `workouts(start_ts)` were added because the existing unique indexes start with other columns and could not serve those ranges (checked with `EXPLAIN QUERY PLAN`).

**Known limits.** Overlapping rows of the same stage and source are merged (union); overlap between different stages is not. Sleep uses the offset the row started in. A nap ending before 18:00 counts toward the night that ended that morning. The same workout recorded by two sources is shown once (same activity, overlapping times, best source wins; same-source sessions are never merged). Source classing is by name, so a third-party app with "watch" in its name counts as a Watch.

## Data safety

Synthetic data only in this repo. `.gitignore` blocks `*.db` and Apple export files.

## Overview page layout (v3)

Order: title, headline, index line, category tables, SLEEP, WORKOUTS, heatmaps, DATA. `overviewSections` (cli/overview_render.go) lists the blocks and each starts with its own blank line (`--compact` drops them). SLEEP and WORKOUTS come from `stats.Overview.SleepSummary` / `WorkoutsReport` (the same reports as `healthcli sleep` / `workouts`, left out of `--json` when there is no data) and are drawn by `renderSleepSection` / `renderWorkoutsSection`: compact by default, full with `--full` or when one of them is the only `--category`. A database without sleep or workouts renders no section and no "none" line. Heatmaps: by default one (steps, else exercise time, else active energy); `--full` adds the sleep and workouts calendars inside their sections; `--heatmap a,b,c` draws exactly those, in order, and then the full sections leave their own calendar out. `--weeks` applies to all of them (0 hides them). The older `sleep` and `workouts` keys of the overview JSON stay for compatibility; `heatmaps` holds the metric calendars named in `--heatmap`.

### QA round 2 rules (v3)
- **Bands:** every section opens with the same band (`bandLine`): `━━ NAME ━━ summary ━━━` (`== NAME ==` in ASCII): metric categories, SLEEP, WORKOUTS, CALENDAR (every heatmap, titled "<what> per day"), DATA. One colour legend (`colorLegend`: "cyan changed (no good direction)") for the metric table and the sleep table.
- **Widths:** 50 to 100 columns (`minWidth` 50). Sleep columns come in three tiers (<60, <80, 80+); heatmap legends and notes wrap onto a second line rather than being cut; the SLEEP subtitle drops "(N days ago)", then the long source text, but is never cut with an ellipsis.
- **Sleep data:** each row keeps its own `tz_offset`, so bedtime and wake use the offset of their own row (a clock change in the night is fine). The *main sleep block* is the block of asleep time with the most sleep, blocks being separated by gaps over 3 h; naps are the rest. Bedtime, wake, midpoint, awakenings and the hypnogram use the main block; `Asleep` includes naps and `Nap` says how much. A nap that only another source recorded (and that is over 3 h from the best source's sleep) is added to Asleep and Nap instead of being dropped by the one-source-per-night rule.
- **Workouts:** zero-duration sessions are ignored everywhere. The weekly "VS AVG" needs 3 earlier weeks since the first workout; weeks before it are not zero weeks.
- **JSON:** snake_case keys, durations in seconds, and an absent value is left out (never `null`; the only nulls are the gaps inside `spark` arrays).
- **--category** scopes the headline, the top mover and the legend to the categories shown; with only sleep/workouts there is no metric table, header or legend. `--compact` keeps a one-character `!` `+` `~` flag in the margin.

## Overview colours

Colour in `overview` follows `metrics.Metric.Polarity`: beyond usual (|z| >= 1 and a change of at least 2%, or the metric's `MinDelta`) and in the healthy direction is green ("better"), the other way bold red ("worse"); neutral metrics say "higher" or "lower" in plain text (verdict "changed" in JSON, `~` in the gutter); anything smaller is typical (plain); stale is yellow. Each beyond-usual row prints its word, so colour is never the only signal. This replaces the earlier "never green/red" rule. Words and arrows always repeat the colour, and the code says polarity is a general guide, not medical advice. Rows are only metrics that have samples (the rest are counted as `not_tracked`).

## `today` as a one-day overview

**In-range metrics (`metrics.InRange`).** Walking double support time and walking asymmetry have no good direction but a band, so `stats.RangeVerdict` judges the LEVEL: inside the band before and after a move is "changed" (plain higher or lower, `~`), leaving the band is "worse", returning is "better", and being nearer or farther outside than the usual counts the same way. Sourced limits (Apple white paper, "Measuring Walking Quality Through iPhone Mobility Metrics", 2022): double support "typical walking behavior ranges between 20 and 40 percent, with lower values indicating better balance" (so only 40 is a limit; a low value is not flagged); asymmetry has no healthy range, only the 35% operating point that best separates asymmetric from symmetric walkers (so only 35 is a limit, and it rarely triggers; a tighter one would be invented). Apple's minimal detectable change for double support is about 3 points per device visit, so small moves are noise. Walking steadiness has no range yet: Apple classifies it OK / Low / Very Low but does not publish the numeric cut-offs, and none could be verified (recalled numbers conflict), so it stays higher-is-better until the numbers can be read off the Health app. Metrics that are themselves a percentage (`Unit == "percent"`) report their change in percentage points (`+1.4pt`) because "1% to 2%" is not "+100%".

**Reference ranges (`metrics.Range`).** A range judges a LEVEL against a population; a verdict judges a CHANGE against the person's own usual. Mixing the two is dishonest (72 to 64 bpm resting HR is "better" and both are in range; a fall in a value that is still out of range is not "good"), so for ordinary metrics a range never colours a row and never changes `verdict`. It only adds a plain dim note under the category when the latest value is outside it, and a `range` object (`low`, `high`, `source`, `basis`, `position`: below, within or above) in the overview JSON. A 0 bound means no limit on that side. Three ordinary metrics have one, each one-sided so that athletes and normal sleep values never trip it: resting heart rate above 100 bpm, respiratory rate above 20 per minute, blood oxygen below 95%. Deliberately without one: glucose (imported as a daily average of all readings, not a fasting value, so the ADA fasting range would be false), BMI (a screening tool, wrong for athletes and older adults) and body fat (needs age and sex, which the importer does not read from `<Me>`). Wearable readings are estimates (the watch's blood oxygen error is a few points), so the wording is "above the usual adult limit", never a clinical label, and always "general guide, not medical advice". Decided by a four-seat design council on 2026-11-10. The in-range metrics above are the one exception: there the range is the verdict, because that was a deliberate choice and the source frames the band as the healthy level.

`healthcli today` is a one-day slice of `overview`, not a separate layout. `stats.BuildDay(ctx, conn, day)` returns a `DayReport`: a `MetricRow` (the overview's own row type) for every metric with a reading ON `day`, anchored on that day with the same `fillComparison` as the overview (28 days before, 7 needed, the "too small to matter" rule, polarity verdict); the tracked metrics without a reading (`NoReading`); the sleep report of the night that ended on `day` (nil otherwise) and the day's workouts. The CLI reuses the overview renderers: headline, `categoryBand`, `gutterMark`/`rowPrefix`, `deltaArea`, `latestCell`, `renderSleepSection`, and `wkSessionRows` (shared with the workouts section). Only `dayTable` (USUAL column instead of the trend and age) and the one-day workouts band are new. Fallback to the latest day with data applies to the whole page, and `--date` in the future is an error. `--json` keeps the original fields of `todayReport` and only adds fields (see `healthcli today --help`).
