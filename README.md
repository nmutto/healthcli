# healthcli

Import your Apple Health export into a SQLite database on your own machine, then look at it from the terminal: a one-screen overview, today's numbers against your baseline, trends over time, and plain SQL for everything else.

> **Status: v0.1.0, not yet released.** Built and tested on synthetic data only. It has **not been tried on a real Apple Health export yet**, so expect rough edges and please report them.

## Privacy

- Everything stays on your computer. Your data lives in one SQLite file.
- No telemetry, no analytics, no accounts, no update checks.
- healthcli makes **no network connections**. It never needs to.
- Your export file is only ever read, never modified.

## Install

**Homebrew** (macOS and Linux) — *once the tap is published* (not yet):

```sh
brew install nmutto/tap/healthcli
```

**Go** (needs a recent Go toolchain):

```sh
go install github.com/nmutto/healthcli/cmd/healthcli@latest
```

**Release binaries** — *once a release is published*: download the `tar.gz` for your system (macOS or Linux, Intel/amd64 or ARM/arm64) from the GitHub Releases page, check it against `checksums.txt`, and put `healthcli` somewhere on your `PATH`:

```sh
tar -xzf healthcli_*_darwin_arm64.tar.gz
sudo mv healthcli /usr/local/bin/
```

The release binaries are **not code-signed** (see the FAQ for the macOS workaround).

## Export your data from the iPhone

1. Open the **Health** app.
2. Tap your **profile picture** (top right).
3. Scroll down and tap **Export All Health Data**, then confirm.
4. Wait: a large history can take several minutes. Then share the resulting `export.zip` to your Mac (AirDrop works well).

You can pass `export.zip` straight to healthcli, or the `export.xml` inside it.

## Quickstart

The output below is real, produced by running the tool on the synthetic sample in `testdata/small` (a made-up person, December 2024).

Import it (use `--db` to choose a database file; without it, healthcli uses a per-user default, see below):

```console
$ healthcli --db demo.db import apple testdata/small/export.xml
Read:       1244 records
Inserted:   1232
Duplicates: 9 (already in the database)
Skipped:    3
  bad_date             1
  missing_value        1
  unknown_type         1
Elapsed:    0.0s
```

Importing the same file again is safe and adds nothing:

```console
$ healthcli --db demo.db import apple testdata/small/export.xml
Read:       1244 records
Inserted:   0
Duplicates: 1241 (already in the database)
Skipped:    3
  ...
```

Log a reading by hand:

```console
$ healthcli --db demo.db log weight 72.5
Logged body_mass = 72.5 kg at 2026-11-08 17:47
```

One screen with every metric, last night's sleep, recent workouts and how complete your data is (`overview`):

```console
$ healthcli --db demo.db overview
 healthcli overview                              db: demo.db · imported 1 d ago
 ──────────────────────────────────────────────────────────────────────────────
 3 typical vs your 28-day usual

 METRIC          LATEST        VS 28-DAY USUAL               AGE
 VITALS
 Avg heart rate  79 bpm        ▼ -1%             ···░●░░···  today
 ACTIVITY
 Steps           19,415 steps  ▲ +2%             ···░░●░···  today
 BODY
 Weight          83.6 kg       ▲ +1%             ···░░░░●··  today
 green better / red worse / cyan changed (no good direction)

 SLEEP    no sleep recorded in the last 30 days
 WORKOUTS none in the last 30 days

 CALENDAR step count per day, 12 weeks to 2026-11-08
     Aug Sep     Oct     Nov
 Mon · · · · · · · · ▁ ▇ ▃ ▁
 Tue · · · · · · · · ▇ █ ▃ █
 Wed · · · · · · · · ▃ ▅ ▁ ▁
 Thu · · · · · · · · ▇ ▃ ▃ ▇
 Fri · · · · · · · · ▇ █ ▅ ▁
 Sat · · · · · · · · ▁ ▇ ▅ █
 Sun · · · · · · · · ▃ ▅ █ ▅ ◂

 ▁ ≤16,838  ▃ ≤18,863  ▅ ≤19,498  ▇ ≤21,359  █ >21,359
 · no data   ◂ today   steps per day

 DATA     1,802 samples
          range: 2026-10-12 → 2026-11-08
          sources: other 100%
          last import: 1,802 added · 0 duplicates
          days missing: Weight 18 of 28
 not tracked in your data: 27 metrics
```

This example is **synthetic**: it is the real output for the generated "sparse" fixture (a fitness band that records only heart rate, steps and weight), with dates shifted to look current. A device that records more gets more rows, grouped under small VITALS / ACTIVITY / BODY / MOBILITY headings; metrics your data does not contain are never shown, only counted in the last line (`--json` lists them as `not_tracked`).

Each metric is compared with its own 28-day usual and shows its own newest day and age. When a value is beyond your usual (1+ standard deviation, and a change of at least 2% or a small per-metric minimum) it is coloured by which direction is generally healthy for that metric: **green** and the word "better", **red** and "worse". Metrics with no good direction (weight, respiratory rate, wrist temperature) are **cyan** and carry the word "changed". Every beyond-usual row has its word, so nothing depends on colour; typical rows are plain. Stale readings are yellow. The legend is the line under the table.

> The better/worse direction is a general guide, not medical advice. What is healthy for one person (an athlete's low resting heart rate, deliberate weight change) can differ.

The calendar after the table shows daily steps (else exercise time, else active energy); `--weeks N` sets its length (default 12, `0` hides it) and `--heatmap METRIC` picks another metric. On a pipe, or with `--ascii`, the output is plain ASCII; `--no-color` and `NO_COLOR` turn colour off, and `--json` prints the same report as JSON, including each metric's polarity and verdict, `not_tracked` and the heatmap grid.

A day's summary against your recent baseline:

```console
$ healthcli --db demo.db today --date 2024-12-13
2024-12-13 (Friday)

Steps               22,019 steps  baseline 19,784             +2,236 (+11.3%)  above
Active energy       881 kcal      baseline 791                +89 (+11.3%)     above
Avg heart rate      76 bpm        baseline 75                 +1 (+0.8%)       typical
Resting heart rate  59 bpm        baseline 57                 +2 (+4.3%)       above
HRV                 62 ms         baseline 47                 +16 (+33.2%)     above
Weight              78.5 kg       baseline n/a (need 7 days)

Sleep
  Asleep 6h 48m  (core 4h 14m, deep 1h 36m, REM 58m, awake 16m, in bed 7h 16m)

Workouts
  Running  18:00  51m  8.5 km  551 kcal
```

Without `--date`, `today` uses today's date.

A metric over time:

```console
$ healthcli --db demo.db trend steps --days 14 --date 2024-12-14
Steps, last 14 days (2024-12-01 to 2024-12-14)

▁▇▆▅▆▃▂▂▅▄▄█▇▇

min 16,525 steps   max 23,283 steps   mean 20,101 steps   (14 of 14 days with data)
Last 7 days vs previous 7: 20,640 steps vs 19,563 steps  (+1,078 steps, +5.5%)
Baseline (28 days): 20,101 steps
```

Metrics: `heart_rate` (`hr`), `resting_heart_rate`, `step_count` (`steps`), `hrv_sdnn`, `body_mass` (`weight`), `active_energy`, and `sleep`.

Plain SQL (read-only; only `SELECT` is allowed):

```console
$ healthcli --db demo.db query "SELECT metric, count(*) AS n FROM samples GROUP BY metric ORDER BY n DESC"
metric              n
step_count          448
heart_rate          330
active_energy       224
hrv_sdnn            14
resting_heart_rate  14
body_mass           6
```

Every command accepts `--json` for scripts:

```console
$ healthcli --db demo.db query "SELECT metric, count(*) AS n FROM samples GROUP BY metric ORDER BY n DESC LIMIT 2" --json
[
  {"metric": "step_count", "n": 448},
  {"metric": "heart_rate", "n": 330}
]
```

### Calendar heatmap

`healthcli heatmap [metric]` draws a GitHub-style calendar: a row per weekday, a column per week, newest on the right. Shades are the metric's own quintiles over the window, and the legend gives the actual cut points. A day without data is a dot, never the lowest shade; `◂` marks today. The default metric is `step_count`, `--weeks` sets the maximum (default 12; a narrow terminal drops the oldest weeks), and `--json` prints the grid.

The output below is from a synthetic test database (made-up step counts), not real data:

```console
$ healthcli heatmap
step count per day, 12 weeks to 2026-11-04
    Aug Sep     Oct     Nov
Mon █ ▇ ▅ ▃ ▁ ▇ ▅ ▃ ▁ █ ▅ ▃
Tue ▇ ▅ ▃ ▁ █ ▅ ▃ ▁ █ ▇ ▅ ▁
Wed ▅ ▃ ▁ █ ▇ ▅ ▁ █ ▇ ▅ ▃ ▁ ◂
Thu ▅ ▁ █ ▇ ▅ ▃ ▁ ▇ ▅ ▃ ▁
Fri ▃ ▁ ▇ ▅ ▃ ▁ █ ▅ ▃ ▁ █
Sat ▁ █ ▅ ▃ ▁ █ ▇ ▅ ▁ █ ▇
Sun █ ▇ ▅ ▁ █ ▇ ▅ ▃ ▁ ▇ ▅

▁ ≤4,950  ▃ ≤6,900  ▅ ≤9,500  ▇ ≤11,450  █ >11,450
· no data   ◂ today   steps per day
```

On a pipe or with `--ascii` the same chart uses plain characters:

```console
$ healthcli heatmap --ascii
step count per day, 12 weeks to 2026-11-04
    Aug Sep     Oct     Nov
Mon @ # + - . # + - . @ + -
Tue # + - . @ + - . @ # + .
Wed + - . @ # + . @ # + - . <
Thu + . @ # + - . # + - .
Fri - . # + - . @ + - . @
Sat . @ + - . @ # + . @ #
Sun @ # + . @ # + - . # +

. <=4,950  - <=6,900  + <=9,500  # <=11,450  @ >11,450
_ no data   < today   steps per day
```

### Where is my data?

If you don't pass `--db`, the database is `healthcli.db` in:

- `$HEALTHCLI_DB`, if you set it (a full file path), otherwise
- `$XDG_DATA_HOME/healthcli/`, if `XDG_DATA_HOME` is set, otherwise
- macOS: `~/Library/Application Support/healthcli/`
- elsewhere: `~/.local/share/healthcli/`

Delete that file to start over.

## Shell completions

If you installed with Homebrew, completions are set up for you. Otherwise generate them yourself:

```sh
# zsh (add to ~/.zshrc, after compinit)
source <(healthcli completion zsh)

# bash
source <(healthcli completion bash)

# fish
healthcli completion fish > ~/.config/fish/completions/healthcli.fish
```

Release archives also ship ready-made files in `completions/`.

## FAQ and limitations

**Which data wins if my Watch and iPhone both record steps?** The Apple Watch takes priority over the iPhone, so you aren't double-counted. Overlapping sleep records are merged and duplicate workouts from two sources are collapsed.

**Has it been tested on real exports?** No. Development used only synthetic data generated by `tools/genfixtures`. Real exports are bigger and messier; if something breaks, that is useful to know.

**Why does macOS say the app can't be opened / is from an unidentified developer?** The release binaries are not signed or notarized for v0.1. After downloading, remove the quarantine flag once:

```sh
xattr -d com.apple.quarantine /path/to/healthcli
```

Installing with Homebrew or `go install` avoids this.

**Does it write to my Health app?** No. It reads an export file and writes only its own SQLite database.

**Which Health data is supported?** Heart rate, resting heart rate, HRV, steps, active energy, body mass, sleep and workouts. Other record types are skipped and counted in the import summary (`unknown_type`).

## Development

```sh
go build ./cmd/healthcli
go vet ./...
go test -race ./...
```

Generate synthetic fixtures (no real data is ever needed or committed):

```sh
go run ./tools/genfixtures --size small --seed 1 --out testdata/small --flat
```

Try a local release build without publishing anything:

```sh
goreleaser release --snapshot --clean --skip=publish
```

See [docs/release.md](docs/release.md) for how a real release is cut, and [docs/design.md](docs/design.md) for how the code is organised. Changes are listed in [CHANGELOG.md](CHANGELOG.md).

## Licence

[MIT](LICENSE) © Niccolo Muttoni
