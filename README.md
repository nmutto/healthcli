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

One screen with every metric, last night's sleep, recent workouts, an activity calendar and how complete your data is (`overview`):

```console
$ healthcli --db demo.db overview
 healthcli overview                              db: demo.db · imported 1 d ago
 ──────────────────────────────────────────────────────────────────────────────
 3 better, 3 worse, 1 changed, 12 typical vs your 28-day usual · Resting HR +6%
 VITALS 9 !1 · ACTIVITY 8 · BODY 4 · MOBILITY 5 !2 · SLEEP · WORKOUTS

   METRIC                      LATEST          VS 28-DAY USUAL               AGE

 ━━ VITALS ━━ 9 metrics · 1 better · 1 worse · 1 changed · 1 stale ━━━━━━━━━━━━
   Avg heart rate              76 bpm          ▲ +2%             ···░░░●···
 ! Resting HR                  60 bpm          ▲ +6% worse       ···░░░░●··
   HRV                         46 ms           ▼ -4%             ···░●░░···
 ~ Respiratory rate            15.6/min        ▲ +9% changed     ···░░░░●··
   Wrist temperature           36.0 °C         – 0%              ···░░●░···
   Blood oxygen                97%             – 0%              ···░░●░···
   VO2 max                     41.9 mL/kg/min  stale                         6 d
 + Walking heart rate          90 bpm          ▼ -4% better      ···●░░░···
   Heart rate recovery         26 bpm          need 7d (have 6)

 ━━ ACTIVITY ━━ 8 metrics · 2 better ━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━
 + Steps                       21,999 steps    ▲ +10% better     ···░░░●···
 + Active energy               880 kcal        ▲ +10% better     ···░░░●···
   Resting energy              1,730 kcal      ▲ +2%             ···░░░●···
   Exercise time               43 min          ▲ +16%            ···░░●░···
   Stand time                  364 min         ▼ -12%            ···░●░░···
   Flights climbed             8 flights       ▼ -39%            ···●░░░···
   Walking + running distance  6.5 km          – 0%              ···░░●░···
   Time in daylight            53 min          ▲ +14%            ···░░●░···

 ━━ BODY ━━ 4 metrics · not enough data ━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━
   Weight                      78.5 kg         need 7d (have 4)
   Body fat                    18.5%           need 7d (have 4)
   BMI                         24.0            need 7d (have 4)
   Lean body mass              63.2 kg         need 7d (have 4)

 ━━ MOBILITY ━━ 5 metrics · 2 worse · 1 stale ━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━
   Walking speed               5.5 km/h        ▲ +2%             ···░░●░···
   Step length                 73 cm           ▲ +2%             ···░░░●···
 ! Walking asymmetry           4.9%            ▲ +38% worse      ···░░░░●··
 ! Double support time         30.7%           ▲ +7% worse       ···░░░░●··
   Walking steadiness          94%             stale                         6 d
 green better / red worse / cyan changed (no good direction)

 SLEEP  Sun 8 Nov · Watch stages, iPhone in bed
 ──────────────────────────────────────────────────────────────────────────────
       23:12                                                              06:30
 awake                       ░░░                        ░░                     
 REM                    ▓▓▓▓▓                      ▓▓▓▓▓                     ▓▓
 core  ▒▒▒▒▒▒▒▒    ▒▒▒▒▒        ▒▒▒▒▒▒▒▒     ▒▒▒▒▒▒       ▒▒▒▒▒▒▒▒▒    ▒▒▒▒▒▒  
 deep          ████                     █████                      ████        
              0         1         2         3         4         5         6

 STAT         LAST NIGHT      7 NIGHTS      VS 28-NIGHT USUAL
 Asleep       7h 01m          7h 08m        – typical
 In bed       7h 30m          7h 37m        – typical
 Efficiency   94%             94%           – typical
 Awake        17m 2x          20m 2x        – typical
 Deep         1h 27m 21%      1h 30m        – typical
 REM          1h 18m 19%      1h 17m        – typical
 Bedtime      23:12           23:12         – typical
 Midpoint     02:51           02:54         – typical
 Sleep debt   59m short last night, 7 n: 6h 05m short vs 8h target

 WORKOUTS ─────────────────────────────────────────────────────────────────────
 28 d  7 sessions  6h 13m  3,243 kcal  71.1 km  active 7 of 28 days

 TYPE      N TIME       KCAL     KM SHARE
 running   3 2h 49m    1,828   28.1 █████████░░░░░░░░░░░  45%
 cycling   2 1h 44m    1,039   34.6 ██████░░░░░░░░░░░░░░  28%
 walking   2 1h 40m      375    8.3 █████░░░░░░░░░░░░░░░  27%

 RECENT
 Sat Nov 07 running     51m  8.5 km 6:00 /km  551 kcal
 Thu Nov 05 cycling  1h 04m 21.3 km 20.0 km/h 638 kcal
 Tue Nov 03 walking     28m  2.3 km 12:00 /km 104 kcal

 CALENDAR step count per day, 12 weeks to 2026-11-08
     Aug Sep     Oct     Nov
 Mon · · · · · · · · · · ▁ ▁
 Tue · · · · · · · · · · ▇ ▅
 Wed · · · · · · · · · · ▇ ▃
 Thu · · · · · · · · · · ▅ ▃
 Fri · · · · · · · · · · ▅ █
 Sat · · · · · · · · · · ▃ █
 Sun · · · · · · · · · · ▁ ▇ ◂

 ▁ ≤17,731  ▃ ≤19,464  ▅ ≤20,972  ▇ ≤21,999  █ >21,999
 · no data   ◂ today   steps per day

 DATA     1,271 samples, 14 sleep nights
          range: 2026-10-26 → 2026-11-08
          sources: Watch 74%, iPhone 26%
          last import: 1,468 added · 9 duplicates
          skipped: 3 (bad_date 1, missing_value 1, unknown_type 1)
          days missing: Weight 8 of 13, Body fat 8 of 13, BMI 8 of 13
 not tracked in your data: 4 metrics
```

This example is **synthetic**: it is the real output for the generated "small" fixture (a Watch and an iPhone), with dates shifted to look current. A device that records less gets fewer rows: metrics your data does not contain are never shown, only counted in the last line (`--json` lists them as `not_tracked`), and a database with no sleep or no workouts has no SLEEP or WORKOUTS section at all. Metrics are grouped in VITALS / ACTIVITY / BODY / MOBILITY bands; a `!` / `+` / `~` in the left gutter marks worse / better / changed rows, and the index line under the headline counts the worse ones. `--category vitals,sleep` shows only some sections, `--compact` drops the blank lines.

`overview --full` shows everything: the sleep table with every row, the weekly workout bars and personal bests, and a heatmap for sleep, workouts and activity. An excerpt (the middle of the page):

```console
$ healthcli --db demo.db overview --full
 SLEEP  Sun 8 Nov · Watch stages, iPhone in bed
 ──────────────────────────────────────────────────────────────────────────────
       23:12                                                              06:30
 awake                       ░░░                        ░░                     
 REM                    ▓▓▓▓▓                      ▓▓▓▓▓                     ▓▓
 core  ▒▒▒▒▒▒▒▒    ▒▒▒▒▒        ▒▒▒▒▒▒▒▒     ▒▒▒▒▒▒       ▒▒▒▒▒▒▒▒▒    ▒▒▒▒▒▒  
 deep          ████                     █████                      ████        
              0         1         2         3         4         5         6

 STAT         LAST NIGHT      7 NIGHTS      VS 28-NIGHT USUAL
 Asleep       7h 01m          7h 08m        – typical
 In bed       7h 30m          7h 37m        – typical
 Efficiency   94%             94%           – typical
 Awake        17m 2x          20m 2x        – typical
 Latency      ~12m            12m           – typical
 Deep         1h 27m 21%      1h 30m        – typical
 REM          1h 18m 19%      1h 17m        – typical
 Core         4h 16m 61%      4h 18m        – typical
 Bedtime      23:12           23:12         – typical
 Wake         06:30           06:37         – typical
 Midpoint     02:51           02:54         – typical
 Regularity   ±1m bed ±8m mid (13 n)
 Sleep debt   59m short last night, 7 n: 6h 05m short vs 8h target
 green better / red worse / cyan neutral

 time asleep per night (a calendar needs 14 nights, have 13)
 Tue Wed Thu Fri Sat Sun Mon Tue Wed Thu Fri Sat Sun
 7h  7h  7h  7h  7h  7h  7h  7h  7h  7h  7h  7h  7h
 ▆▆▆ ▆▆▆ ▆▆▆ ▆▆▆ ▆▆▆ ▆▆▆ ▆▆▆ ▆▆▆ ▆▆▆ ▆▆▆ ▆▆▆ ▆▆▆ ▆▆▆

 WORKOUTS ─────────────────────────────────────────────────────────────────────
 28 d  7 sessions  6h 13m  3,243 kcal  71.1 km  active 7 of 28 days

 TYPE      N TIME       KCAL     KM SHARE
 running   3 2h 49m    1,828   28.1 █████████░░░░░░░░░░░  45%
 cycling   2 1h 44m    1,039   34.6 ██████░░░░░░░░░░░░░░  28%
 walking   2 1h 40m      375    8.3 █████░░░░░░░░░░░░░░░  27%

 RECENT
 Sat Nov 07 running     51m  8.5 km 6:00 /km  551 kcal
 Thu Nov 05 cycling  1h 04m 21.3 km 20.0 km/h 638 kcal
 Tue Nov 03 walking     28m  2.3 km 12:00 /km 104 kcal
 Sun Nov 01 running     58m  9.7 km 6:00 /km  632 kcal
 Fri Oct 30 cycling     40m 13.4 km 20.0 km/h 402 kcal

 BESTS (90 d)
 longest   running 1h 00m, Oct 26 *new*
 fastest   running 6:00 /km, Oct 26 *new*
 furthest  running 9.9 km, Oct 26 *new*
 biggest   running 645 kcal, Oct 26 *new*

 workout minutes per day, 12 weeks to 2026-11-08
     Aug Sep     Oct     Nov
 Mon                     ▅ ·
 Tue                     · ▁
 Wed                     █ ·
 Thu                     · ▇
 Fri                     ▁ ·
 Sat                     · ▃
 Sun                     ▅ · ◂

 ▁ ≤40m  ▃ ≤51m  ▅ ≤1h 00m  ▇ ≤1h 04m  █ >1h 04m
 · rest day   ◂ today   per day
  ...
```

The same full sections are available on their own as `healthcli sleep` and `healthcli workouts`.

Each metric is compared with its own 28-day usual and shows its own newest day and age. When a value is beyond your usual (1+ standard deviation, and a change of at least 2% or a small per-metric minimum) it is coloured by which direction is generally healthy for that metric: **green** and the word "better", **red** and "worse". Metrics with no good direction (weight, respiratory rate, wrist temperature) are **cyan** and carry the word "changed". Every beyond-usual row has its word, so nothing depends on colour; typical rows are plain. Stale readings are yellow. The legend is the line under the table.

> The better/worse direction is a general guide, not medical advice. What is healthy for one person (an athlete's low resting heart rate, deliberate weight change) can differ.

The calendar at the end shows daily steps (else exercise time, else active energy); `--weeks N` sets its length (default 12, `0` hides all calendars) and `--heatmap steps,sleep,workouts,METRIC` draws exactly the calendars you name, in that order. On a pipe, or with `--ascii`, the output is plain ASCII; `--no-color` and `NO_COLOR` turn colour off, and `--json` prints the same report as JSON, including each metric's polarity and verdict, `not_tracked` and the heatmap grid.

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

### Workouts

`healthcli workouts [--days N] [--weeks N] [--by type]` shows totals per activity type with a share bar, weekly training time (the current week is marked and gets no change figure), personal bests over 90 days (an activity needs 3 sessions), the newest sessions and a calendar of workout minutes per day, where empty days after your first workout are rest days. `--by type` draws each day's main activity as a letter. Columns with no data (no calories or distance recorded) disappear, and with no workouts you get one line, not an error. `--json` prints the report.

Synthetic data, not real:

```console
$ healthcli workouts
 WORKOUTS ─────────────────────────────────────────────────────────────────────
 28 d  16 sessions  16h 44m  6,820 kcal  187.5 km  active 16 of 28 days

 TYPE      N TIME       KCAL     KM SHARE
 cycling   6 7h 57m    3,450  126.0 ██████████░░░░░░░░░░  48%
 running   5 5h 00m    2,740   49.6 ██████░░░░░░░░░░░░░░  30%
 walking   3 2h 09m      630   11.9 ███░░░░░░░░░░░░░░░░░  13%
 yoga      2 1h 38m        -      - ██░░░░░░░░░░░░░░░░░░  10%

 RECENT
 Sat Nov 07 cycling  1h 07m 21.0 km 18.8 km/h 550 kcal
 Fri Nov 06 walking     33m  4.3 km 7:40 /km  210 kcal
 Wed Nov 04 running  1h 10m 11.0 km 6:22 /km  580 kcal

 BESTS (90 d)
 longest   cycling 1h 32m, Oct 13 *new*
 fastest   running 3:11 /km, Sep 30
 furthest  cycling 21.0 km, Sep 03
 biggest   cycling 700 kcal, Sep 23
...
```

### Where is my data?

If you don't pass `--db`, the database is `healthcli.db` in:

- `$HEALTHCLI_DB`, if you set it (a full file path), otherwise
- `$XDG_DATA_HOME/healthcli/`, if `XDG_DATA_HOME` is set, otherwise
- macOS: `~/Library/Application Support/healthcli/`
- elsewhere: `~/.local/share/healthcli/`

Delete that file to start over.

### Sleep

`healthcli sleep` shows last night as a hypnogram (awake, REM, core and deep over the night; a short awakening still shows), then last night, your last 7 nights and the change against your usual over `--nights` nights (default 28), with bedtime regularity and sleep debt against `--sleep-target` (default `8h`). A calendar of the nights follows (`--weeks`, `0` hides it; `--by asleep|bedtime|efficiency`); with fewer than 14 nights you get a small bar per night instead. Only what your data holds is shown: a device that records just "asleep" gets a single bar, in-bed time alone shows no sleep numbers, and rows without data are left out (one dim `not available:` line names them). Green is better, red worse, cyan has no good direction, always with a word. A night is the sleep that ends on a date (18:00 to 18:00). Output below is synthetic.

```
$ healthcli sleep
 SLEEP  Wed 4 Nov · Watch stages, iPhone in bed
 ──────────────────────────────────────────────────────────────────────────────
       00:05                                                              06:00
 awake                                      ░                  ░               
 REM                                    ▓▓▓▓             ▓▓▓▓▓▓           ▓▓▓▓▓
 core  ▒▒▒▒▒▒▒▒▒▒▒▒▒▒         ▒▒▒▒▒▒▒▒▒▒     ▒▒▒▒▒▒▒▒▒▒▒▒       ▒▒▒▒▒▒▒▒▒▒     
 deep                █████████                                                 
                  1           2           3           4           5

 STAT         LAST NIGHT      7 NIGHTS      VS 28-NIGHT USUAL
 Asleep       5h 50m          6h 08m        ▼ -11% worse
 In bed       6h 03m          6h 29m        ▼ -12% changed
 Efficiency   96%             94%           – typical
 Awake        5m 0x           9m 1x         – typical
 Latency      ~8m             11m           – typical
 Deep         45m 13%         45m           – typical
 REM          1h 15m 21%      1h 14m        – typical
 Core         3h 50m 66%      4h 08m        ▼ -15% changed
 Bedtime      00:05           23:58         ▲ +49m later
 Wake         06:00           06:15         – typical
 Midpoint     03:03           03:07         ▲ +26m later
 Regularity   ±51m bed ±34m mid (14 n)   ▲ worse than usual 39m
 Sleep debt   2h 10m short last night, 7 n: 13h 07m short vs 8h target
 green better / red worse / cyan neutral

 time asleep per day, 12 weeks to 2026-11-04
     Aug Sep     Oct     Nov
 Mon · · · · · · ▁ ▅ ▇ ▅ ▇ ▁
 Tue · · · · · · ▇ ▃ ▇ ▃ ▇ ▁
 Wed · · · · · · ▅ ▃ ▅ ▁ ▇ ▁ ◂
 Thu · · · · · · ▅ ▇ ▅ ▇ ▁
 Fri · · · · · · ▃ ▇ ▃ ▇ ▁
 Sat · · · · · ▅ ▇ ▅ ▇ ▇ ▃
 Sun · · · · · ▃ ▇ ▃ ▇ ▇ ▁

 ▁ ≤6h 36m  ▃ ≤6h 52m  ▅ ≤7h 10m  ▇ ≤7h 20m  █ >7h 20m
 · no data   ◂ today   per day
```

With only "asleep" data (no stages, no in-bed):

```
$ healthcli sleep
 SLEEP  Wed 4 Nov · iPhone, no stages (unspecified asleep only)
 ──────────────────────────────────────────────────────────────────────────────
 ▒▒▒▒▒▒▒▒▒▒▒▒▒▒▒▒▒▒▒▒▒▒▒▒▒▒▒▒▒▒▒▒▒▒▒▒▒▒▒▒▒▒▒▒▒▒▒▒▒▒▒▒▒▒▒▒▒▒▒▒▒▒▒▒▒▒▒▒▒▒▒▒▒▒▒▒▒▒
 asleep 5h 55m   00:05 → 06:00   midpoint 03:02           (stages not recorded)

 STAT         LAST NIGHT      6 NIGHTS      VS USUAL
 Asleep       5h 55m          6h 18m        need 7 nights (have 6)
 Bedtime      00:05           00:00         need 7 nights (have 6)
 Wake         06:00           06:18         need 7 nights (have 6)
 Midpoint     03:03           03:09         need 7 nights (have 6)
 Regularity   ±19m bed ±13m mid (6 n)
 Sleep debt   2h 05m short last night, 6 n: 10h 15m short vs 8h target
 not available: in bed, efficiency, awake, latency, deep, REM, core

 time asleep per night (a calendar needs 14 nights, have 6)
 Fri Sat Sun Mon Tue Wed
 6h  7h  7h  6h  6h  6h
 ▅▅▅ ▆▆▆ ▆▆▆ ▅▅▅ ▅▅▅ ▅▅▅
```

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
