-- 0002_read_indexes.sql: indexes for the read side (internal/stats).
--
-- samples needs nothing new: its UNIQUE (metric, start_ts, ...) index already
-- serves "metric X with start_ts between A and B".
--
-- sleep_stages: a night belongs to the local date its sleep ENDS, so nights
-- are looked up by end_ts. The existing unique index starts with start_ts,
-- which cannot answer an end_ts range without scanning the whole table.
CREATE INDEX sleep_stages_end_ts ON sleep_stages (end_ts);

-- workouts: looked up by start_ts. The unique index starts with activity, so
-- a date range ("all workouts this week") would otherwise scan every row.
CREATE INDEX workouts_start_ts ON workouts (start_ts);
