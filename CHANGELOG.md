# Changelog

All notable changes to this project are documented here.
The format is based on [Keep a Changelog](https://keepachangelog.com/en/1.1.0/),
and this project adheres to [Semantic Versioning](https://semver.org/spec/v2.0.0.html).

## [Unreleased]

## [0.1.0] - Unreleased

First release (not yet published).

### Added
- `healthcli import apple` streams an Apple Health `export.xml` or `export.zip` into a local SQLite database; re-importing the same file adds nothing.
- `healthcli log` records a manual reading (weight, heart rate, steps, ...).
- `healthcli today` shows a one-day summary with comparison to your recent baseline.
- `healthcli trend` shows a metric over the last N days.
- `healthcli query` runs read-only SQL against the database; `--json` output on every command.
- `healthcli version` prints version, commit and build date.
- Shell completions (bash, zsh, fish, PowerShell) via `healthcli completion`.
- Release tooling: GoReleaser config for macOS and Linux (amd64, arm64).
