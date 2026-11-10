# Changelog

All notable changes to this project are documented here.
The format is based on [Keep a Changelog](https://keepachangelog.com/en/1.1.0/),
and this project adheres to [Semantic Versioning](https://semver.org/spec/v2.0.0.html).

## [Unreleased]

## [0.1.0] - Unreleased

First release (not yet published).

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
