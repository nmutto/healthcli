#!/bin/sh
# Regenerates the README screenshots and demo GIF in docs/media/ from the
# synthetic "small" fixture (no real health data). Needs: go, sqlite3, vhs.
# Run from the repo root:  tools/media.sh
set -eu

work=$(mktemp -d)
trap 'rm -rf "$work"' EXIT

go build -o "$work/bin/healthcli" ./cmd/healthcli
go run ./tools/genfixtures --size small --seed 1 --devices --flat --out "$work/fx"
export PATH="$work/bin:$PATH" HEALTHCLI_DB="$work/demo.db"

# Import once to build the database, then shift it so the newest sample is
# yesterday (whole days, so clock times stay the same). The demo tape
# imports the file again live; that run finds every record already stored,
# so the tape starts from an empty database instead.
healthcli import apple "$work/fx/export.xml" >/dev/null
# The fixture's last night is iPhone-only on purpose (to test fallbacks); drop
# it so the screenshots show the usual Watch stages.
sqlite3 "$HEALTHCLI_DB" "DELETE FROM sleep_stages WHERE end_ts > (SELECT max(end_ts) FROM sleep_stages WHERE device_model = 'Watch')"
max=$(sqlite3 "$HEALTHCLI_DB" "SELECT max(end_ts) FROM samples")
shift=$(( ( ( $(date +%s) - max ) / 86400 - 1 ) * 86400 ))
for t in samples sleep_stages workouts; do
  sqlite3 "$HEALTHCLI_DB" "UPDATE $t SET start_ts = start_ts + $shift, end_ts = end_ts + $shift"
done
cp "$HEALTHCLI_DB" "$work/shifted.db"

export SHIFTED_DB="$work/shifted.db" EXPORT_XML="$work/fx/export.xml"
# demo.tape starts from an empty database (it imports live); the screenshot
# tapes copy the shifted one themselves.
for tape in docs/media/*.tape; do
  rm -f "$HEALTHCLI_DB"
  vhs "$tape"
done
