# Importer

`healthcli import apple <export.zip|export.xml>` streams an Apple Health export into SQLite.
Code: `internal/importer` (`importer.go` = parse loop, `convert.go` = record -> row, `store.go` = batching and zip).

## How it works

1. `openExport` opens the file. Files starting with `PK` are zips: the `export.xml` entry is read straight out of the archive (no temp file; `export_cda.xml` is ignored).
2. `parse` calls `xml.Decoder.Token()` in a loop. Only `<Record>` and `<Workout>` start tags matter; everything else (DTD, `Me`, children such as `MetadataEntry`) streams past. Nothing is kept, so memory stays flat.
3. `convert.go` turns attributes into a row or a skip reason:
   - Apple type -> our metric via `metrics.FromApple`. The registry in `internal/metrics` is the list (about 30 quantity metrics; run `healthcli trend --help` for the names). Types it does not know are `unknown_type`; the most common ones are listed after the import summary.
   - Units go to the metric's canonical unit (`count/min` -> `bpm`, `lb`/`g` -> `kg`, `kJ` and `Cal` -> `kcal`, `s` -> `ms`); values are rounded to 4 decimals. Values must be plain decimal numbers (no hex or underscores); negative step/energy/distance-style sums and heart rates of 0 or less are `bad_value`.
   - Sleep values -> `sleep_stages` (`in_bed`, `core`, `deep`, `rem`, `awake`, `asleep_unspecified`). Old iOS's plain `...SleepAnalysisAsleep` maps to `asleep_unspecified`.
   - `<Workout>` -> `workouts` in seconds, kcal, metres. Energy and distance come from the `totalEnergyBurned`/`totalDistance` attributes; if an attribute is absent, from the `<WorkoutStatistics>` children (`ActiveEnergyBurned`, first `Distance*` with a sum; kJ, km, mi, ft, yd converted). Attributes win. A missing duration, or a duration without `durationUnit`, falls back to end - start; an energy or distance without a unit is stored as NULL and the workout is kept; missing distance/energy are stored as NULL. Activity names are snake_case without Apple's prefix.
   - Dates become UTC unix seconds; `tz_offset` is the `+ZZZZ` written in that record.
4. `store.go` writes with prepared statements, `INSERT ... ON CONFLICT DO NOTHING`, 5000 rows per transaction. `RowsAffected() == 0` means duplicate.
5. At the end `meta.last_import_ts` is set to the import start time, and `meta.last_import_summary` (JSON: read, inserted, duplicates, skipped) is written. A re-import replaces the summary: it always describes the most recent run, so importing the same file again shows 0 inserted.

## Rules worth knowing

- **Lossless.** iPhone and Watch rows for the same period are both kept (different `source`). Only rows identical in (metric, start, end, source) collapse. Source priority belongs to read-time queries.
- **Idempotent.** A second import of the same file inserts 0 rows (everything is counted as a duplicate).
- **Bad records never abort.** `NaN`, `Inf` and values that overflow to Inf count as `bad_value` (`bad_workout` for workouts). They are counted by reason: `unknown_type`, `missing_value`, `bad_value`, `bad_date` (unparseable or end before start), `bad_unit`, `unknown_sleep_stage`, `bad_workout`.
- **Broken files do abort**, with a clear error (file not found, not a zip, no `<HealthData>`, XML syntax error). Batches already committed stay; re-running the import is safe.
- Progress (`read N records...`) goes to stderr only when stderr is a terminal. `--json` prints the summary on stdout.

## Added 2026-11-11
- The reader replaces invalid UTF-8 and XML-forbidden control characters with U+FFFD and counts them (`invalid_bytes`); a single tag or text run is capped at 16 MiB, zip entries at 64 GiB and archives at 128 GiB.
- The device attribute is kept: `device_model` (text after `model:`) on `samples`, `sleep_stages` and `workouts`. A clash with a stored row that has no model fills it in; the dedupe key and counts are unchanged. The meta key `source_models` maps each source name to its model for the overview.
- `Duplicates` means identical rows; `Conflicts` means the key matched but the value differs (the stored row stays). `read = inserted + duplicates + conflicts + skipped`.
- Out-of-range values are skipped as `implausible_value`, using the per-metric limits in the registry (`TestLimitsTable`); `bad_value` is for non-numbers.
- `ImportContext` stops after the last whole batch when cancelled; `Check(path)` validates the file before any database is opened.

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
