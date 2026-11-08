# Test fixtures

All fixture data is **synthetic**, produced by `tools/genfixtures` from the public
`export.xml` format. No real health data is used anywhere.

```
# committed sample (14 days, ~300KB) -> testdata/small/export.xml
go run ./tools/genfixtures --size small --seed 1 --out testdata/small --flat

# big stress file (4 years, ~740MB XML + zip), gitignored -> testdata/large/
go run ./tools/genfixtures --size large --out testdata/large
```

Output is `DIR/apple_health_export/export.xml` plus `DIR/export.zip` (same layout as a
real export; `--flat` drops the subfolder). Same `--seed` gives byte-identical files.
The XML is streamed to disk, never built in memory.

## Format notes and what each oddity tests

| Oddity | Why it is there |
|---|---|
| `<!DOCTYPE HealthData [ ...DTD... ]>` header | The parser must tolerate Apple's inline DTD. |
| Dates `YYYY-MM-DD HH:MM:SS +ZZZZ` | Offset is part of the string; parse it, don't assume UTC. |
| Timezone shift `+0100` to `-0500` (days 5-9 in small, two weeks each July in large) | Days keep wall-clock times but the offset changes; store absolute instants. |
| Steps and active energy from "Alex’s iPhone" and "Alex’s Apple Watch" for the same hours | Same period, two sources: dedupe/prefer a source, don't double-count. |
| Curly apostrophe (U+2019) in source names | UTF-8 handling. |
| Sleep stages `InBed`, `AsleepCore`, `AsleepDeep`, `AsleepREM`, `Awake`, `AsleepUnspecified` | Modern values; the iPhone writes `InBed` (plus legacy `AsleepUnspecified` every 7th night), the Watch writes stages. Nights cross midnight. |
| Workouts (running/walking/cycling) with `duration`, `totalDistance`, `totalEnergyBurned` and units | Attributes live on the element, with child `MetadataEntry`/`WorkoutStatistics`. |
| `MetadataEntry` / `HeartRateVariabilityMetadataList` children inside some `Record`s | A Record is not always self-closing. |
| Byte-identical resting-HR record repeated on some days | Re-importing / exact duplicates must be harmless. |
| Malformed records: missing `value`, impossible date (`2024-13-45 99:99:99`), unknown type, non-numeric value (`"fast"`) | Still well-formed XML; the importer must skip them and carry on. Small has three (days 2, 7, 12). |

## Gotchas

- `.gitignore` ignores `export.xml` everywhere (safety against real data); only
  `testdata/small/export.xml` is explicitly un-ignored.
- DST is not modelled: the home offset is a constant `+0100`.
