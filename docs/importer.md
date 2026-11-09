# Importer

`healthcli import apple <export.zip|export.xml>` streams an Apple Health export into SQLite.
Code: `internal/importer` (`importer.go` = parse loop, `convert.go` = record -> row, `store.go` = batching and zip).

## How it works

1. `openExport` opens the file. Files starting with `PK` are zips: the `export.xml` entry is read straight out of the archive (no temp file; `export_cda.xml` is ignored).
2. `parse` calls `xml.Decoder.Token()` in a loop. Only `<Record>` and `<Workout>` start tags matter; everything else (DTD, `Me`, children such as `MetadataEntry`) streams past. Nothing is kept, so memory stays flat.
3. `convert.go` turns attributes into a row or a skip reason:
   - Apple type -> our metric via `metrics.FromApple` (heart_rate, resting_heart_rate, step_count, hrv_sdnn, body_mass, active_energy).
   - Units go to the metric's canonical unit (`count/min` -> `bpm`, `lb`/`g` -> `kg`, `kJ` -> `kcal`, `s` -> `ms`); values are rounded to 4 decimals.
   - Sleep values -> `sleep_stages` (`in_bed`, `core`, `deep`, `rem`, `awake`, `asleep_unspecified`). Old iOS's plain `...SleepAnalysisAsleep` maps to `asleep_unspecified`.
   - `<Workout>` -> `workouts` in seconds, kcal, metres. Energy and distance come from the `totalEnergyBurned`/`totalDistance` attributes; if an attribute is absent, from the `<WorkoutStatistics>` children (`ActiveEnergyBurned`, first `Distance*` with a sum; kJ, km, mi, ft, yd converted). Attributes win. A missing duration falls back to end - start; missing distance/energy are stored as NULL. Activity names are snake_case without Apple's prefix.
   - Dates become UTC unix seconds; `tz_offset` is the `+ZZZZ` written in that record.
4. `store.go` writes with prepared statements, `INSERT ... ON CONFLICT DO NOTHING`, 5000 rows per transaction. `RowsAffected() == 0` means duplicate.
5. At the end `meta.last_import_ts` is set to the import start time, and `meta.last_import_summary` (JSON: read, inserted, duplicates, skipped) is written. A re-import replaces the summary: it always describes the most recent run, so importing the same file again shows 0 inserted.

## Rules worth knowing

- **Lossless.** iPhone and Watch rows for the same period are both kept (different `source`). Only rows identical in (metric, start, end, source) collapse. Source priority belongs to read-time queries.
- **Idempotent.** A second import of the same file inserts 0 rows (everything is counted as a duplicate).
- **Bad records never abort.** `NaN`, `Inf` and values that overflow to Inf count as `bad_value` (`bad_workout` for workouts). They are counted by reason: `unknown_type`, `missing_value`, `bad_value`, `bad_date` (unparseable or end before start), `bad_unit`, `unknown_sleep_stage`, `bad_workout`.
- **Broken files do abort**, with a clear error (file not found, not a zip, no `<HealthData>`, XML syntax error). Batches already committed stay; re-running the import is safe.
- Progress (`read N records...`) goes to stderr only when stderr is a terminal. `--json` prints the summary on stdout.

## Benchmark

Synthetic `--size large` fixture from `tools/genfixtures` (741 MB XML, 2,967,522 records, generated into a scratch dir, never committed). Apple M-series laptop, Go 1.27, release build, fresh database.

| Run | Time | Records/s | Peak RSS |
|---|---|---|---|
| `export.xml`, empty DB | 20.2 s | ~147,000 | 23.9 MB |
| `export.zip`, 2nd import (all duplicates) | 18.2 s | ~163,000 | 23.1 MB |

Result: 2,965,741 inserted, 1,732 exact duplicates (the fixture repeats some records on purpose), 49 skipped. The resulting DB is 341 MB. Memory does not grow with file size.

Reproduce:

```
go run ./tools/genfixtures --size large --out "$TMPDIR/large"
go build -o "$TMPDIR/healthcli" ./cmd/healthcli
/usr/bin/time -l "$TMPDIR/healthcli" --db "$TMPDIR/b.db" import apple "$TMPDIR/large/apple_health_export/export.xml"
# or the Go benchmark (records/s):
HEALTH_BENCH_FILE="$TMPDIR/large/apple_health_export/export.xml" \
  go test ./internal/importer -run xxx -bench Import -benchtime 1x -benchmem
```

Most time goes to `encoding/xml` (about 125M allocations for the run; the Go benchmark reports ~5 GB allocated in total, all short-lived garbage, hence the small RSS). If speed ever matters, `Decoder.RawToken` or a hand-written scanner is the next step.

## Metrics registry change

Heart rates are now stored as `bpm` (was `count/min`), so `healthcli log hr` and imported rows agree. `count/min` is still accepted as an input unit.
