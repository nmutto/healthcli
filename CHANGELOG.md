# Changelog

All notable changes to this project are documented here.
The format is based on [Keep a Changelog](https://keepachangelog.com/en/1.1.0/),
and this project adheres to [Semantic Versioning](https://semver.org/spec/v2.0.0.html).

## [0.1.0] - 2026-11-13

First release.

### Changed
- Import robustness: input checked before the database is opened (a failed import into a new database leaves no file), invalid UTF-8 tolerated, zip-bomb and oversized-token guards, Ctrl-C stops cleanly, `--dry-run`, automatic `<db>.bak` copy (`--no-backup`), value clashes counted as `Conflicts`.
- Sources are classified by Apple's device model (migration 0003), falling back to the source name for older imports; re-importing backfills the model.
- One registry-driven plausibility range per metric shared by `import` and `log` (new skip reason `implausible_value`).
- `log` refuses dates before 1990 or far in the future and ambiguous or non-existent local times; `log --delete` removes a manual reading.
- `query` streams, with `--limit` (default 1000) and `--timeout` (default 30s); the rejection message names SELECT, WITH and EXPLAIN.
- Exit codes 0/1/2/3, JSON errors with `--json`, clearer usage errors, metric and flag-value completion, version from build info for `go install`.
- `--date` on `overview`, `sleep`, `workouts` and `heatmap`; `overview` about 3x faster on millions of samples; "imported N d ago" counts calendar days.
- Databases from a newer healthcli are refused; upgrades print a notice and rebuilding migrations take a backup; read commands work on read-only copies.
- Fixed: infinite loop at the end of the calendar range, sleep-source tie depending on map order.

### Added
- `healthcli import apple` streams an Apple Health `export.xml` or `export.zip` into a local SQLite database; re-importing the same file adds nothing. About 30 metrics, sleep stages and workouts are read; skipped records are counted by reason and the most common unknown types are listed.
- `healthcli log` records a manual reading (weight, heart rate, steps, ...), with aliases `hr`, `hrv`, `rhr`, `rr`, `spo2`, `steps`, `temp`, `vo2`, `weight`, `bmi`.
- `healthcli overview`: one screen with every metric your data holds in category bands (vitals, activity, body, mobility), each against its own 28-day usual, with a gutter marker (`!` worse, `+` better, `~` higher/lower), an index line, a SLEEP and a WORKOUTS section, calendars and a DATA summary. `--category`, `--compact`, `--full`, `--heatmap`, `--weeks`, `--ascii`, `--no-color`; `--json` honours `--category`.
- `healthcli today`: a one-day slice of the overview (LATEST, USUAL, VS USUAL), with that night's sleep and the day's workouts; falls back to the latest day with data, never past today.
- `healthcli sleep`: hypnogram, last night / 7 nights / usual table (the usual is the nights before the one shown), efficiency, awakenings, regularity, sleep debt, calendar.
- `healthcli workouts`: totals per type, weekly volume, personal bests, every session of the last 7 days, calendar with rest days.
- `healthcli heatmap [metric]`: GitHub-style calendar for any metric, sleep or workouts, in colour or five ASCII heights.
- `healthcli trend` shows a metric (any registry metric, or `sleep`) over the last N days; `--ascii`, `--no-color`; never wider than the terminal.
- `healthcli query` runs read-only SQL against the database; `--json` output on every command.
- `healthcli version` prints version, commit and build date.
- Shell completions (bash, zsh, fish, PowerShell) via `healthcli completion`.
- Release tooling: GoReleaser config for macOS and Linux (amd64, arm64).
- Reference-range notes: resting heart rate above 100 bpm, respiratory rate above 20/min and blood oxygen below 95% add a plain note under their category. They never colour a row or change its verdict.
- In-range metrics: walking double support time (limit 40%) and walking asymmetry (limit 35%) are judged by their level; a move inside the range says only higher or lower. Walking steadiness has no range yet.
- Metrics that are a percentage show their change in points (`+1.4pt`), not a relative percent.

### Changed
- Verdicts: green `better`, bold red `worse`, and plain `higher`/`lower` (sleep: later/earlier, longer/shorter) where no direction is good. The legend names the markers, so it reads the same without colour.

### Security and hardening
- `query` opens the database read-only and accepts exactly one `SELECT`, `WITH` or `EXPLAIN` statement; `PRAGMA`, `ATTACH` and a second statement are refused.
- Read commands fail with `database not found` instead of creating an empty file; only `import` and `log` create it. The database file is created with mode 0600.
- `log` validates input: finite plain numbers (no hex), percentages 0-100, a blood oxygen hint ("type 97 for 97%"), implausible values refused, `--at` at most one day ahead.
- The importer rejects hex and underscore numbers, negative sums and heart rates of 0 or less, percentages above 100, and handles workouts with missing units; a failed import says how many rows were saved.
