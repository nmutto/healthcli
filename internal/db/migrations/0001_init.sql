-- 0001_init.sql: the first schema for the health database.
--
-- Conventions used by every table below:
--   * Timestamps (*_ts) are UTC Unix seconds stored as INTEGER. Integers
--     sort and compare correctly and never carry timezone ambiguity.
--   * tz_offset is the local offset from UTC in seconds (e.g. +3600 for
--     UTC+01:00) at the moment the record was taken. We keep it so a
--     report can show "local wall-clock time" without guessing.
--   * source is NOT NULL DEFAULT ''. This matters: SQLite treats NULLs as
--     all different from each other inside UNIQUE constraints, so a NULL
--     source would let the same row be imported twice.
--   * STRICT tables make SQLite reject values of the wrong type instead of
--     silently storing e.g. the text '72 bpm' in a REAL column.
--   * Baselines/averages are never stored: they are always computed from
--     the raw rows, so they can't go stale.

-- samples: one numeric measurement (heart rate, steps, weight, ...).
-- metric is our own normalised name (e.g. 'heart_rate'), not Apple's long
-- HKQuantityTypeIdentifier... string.
CREATE TABLE samples (
    id        INTEGER PRIMARY KEY,
    metric    TEXT    NOT NULL,
    start_ts  INTEGER NOT NULL,
    end_ts    INTEGER NOT NULL,
    value     REAL    NOT NULL,
    unit      TEXT    NOT NULL,
    tz_offset INTEGER NOT NULL DEFAULT 0,
    source    TEXT    NOT NULL DEFAULT '',
    -- Re-importing the same export must not create duplicates. The importer
    -- can use INSERT ... ON CONFLICT DO NOTHING against this constraint.
    -- It also creates an index starting with (metric, start_ts), which is
    -- exactly what "metric X between dates A and B" queries need.
    UNIQUE (metric, start_ts, end_ts, source)
) STRICT;

-- sleep_stages: one interval of sleep analysis.
-- stage is one of: 'in_bed', 'awake', 'core', 'deep', 'rem',
-- 'asleep_unspecified'. We do not use a CHECK constraint so a future Apple
-- stage can be stored without a table rebuild; the importer validates it.
CREATE TABLE sleep_stages (
    id        INTEGER PRIMARY KEY,
    stage     TEXT    NOT NULL,
    start_ts  INTEGER NOT NULL,
    end_ts    INTEGER NOT NULL,
    tz_offset INTEGER NOT NULL DEFAULT 0,
    source    TEXT    NOT NULL DEFAULT '',
    UNIQUE (start_ts, end_ts, stage, source)
) STRICT;

-- workouts: one workout session. Units are fixed so nobody has to convert
-- at query time: duration in seconds, energy in kcal, distance in metres.
-- energy_kcal and distance_m may be NULL (not every workout records them).
CREATE TABLE workouts (
    id          INTEGER PRIMARY KEY,
    activity    TEXT    NOT NULL,
    start_ts    INTEGER NOT NULL,
    end_ts      INTEGER NOT NULL,
    duration_s  REAL    NOT NULL,
    energy_kcal REAL,
    distance_m  REAL,
    tz_offset   INTEGER NOT NULL DEFAULT 0,
    source      TEXT    NOT NULL DEFAULT '',
    UNIQUE (activity, start_ts, end_ts, source)
) STRICT;

-- meta: small key/value facts about the database itself, e.g.
-- 'last_import_ts' or 'export_date'. Not for health data.
CREATE TABLE meta (
    key   TEXT PRIMARY KEY,
    value TEXT NOT NULL
) STRICT;
