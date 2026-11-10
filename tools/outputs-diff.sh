#!/usr/bin/env bash
# Compare what two healthcli binaries print, case by case.
#
#   tools/outputs-diff.sh <old-binary> <new-binary> [workdir]
#
# Builds a deterministic set of synthetic databases once (genfixtures --size
# small --seed 7, shifted +694 days as in docs/fixtures.md, plus a database
# with in-bed-only and awake-only sleep, an empty one and a one-day one), gives
# each binary its own copy, runs both over a matrix of commands x modes and
# compares the outputs byte for byte. Exit 0: identical. Exit 1: some case
# differs (the first ones are printed). Exit 2: it could not run.
#
# Nothing but the fixtures is read, and the real database is never touched
# (every command gets --db). The output depends on the date only through the
# shifted fixture, so two runs on the same day are identical; the script
# refuses to report a result if midnight passed while it ran.
set -u

if [ $# -lt 2 ] || [ $# -gt 3 ]; then
	echo "usage: $0 <old-binary> <new-binary> [workdir]" >&2
	exit 2
fi
ROOT=$(cd "$(dirname "$0")/.." && pwd)
OLD=$(cd "$(dirname "$1")" && pwd)/$(basename "$1")
NEW=$(cd "$(dirname "$2")" && pwd)/$(basename "$2")
for b in "$OLD" "$NEW"; do
	[ -x "$b" ] || { echo "not an executable: $b" >&2; exit 2; }
done
for tool in sqlite3 script go; do
	command -v "$tool" >/dev/null || { echo "needs $tool on the PATH" >&2; exit 2; }
done
WORK=${3:-$(mktemp -d)}
mkdir -p "$WORK"
WORK=$(cd "$WORK" && pwd)
export WORK
rm -rf "$WORK/out" "$WORK/db" "$WORK/fx" "$WORK/home"
mkdir -p "$WORK/out/old" "$WORK/out/new" "$WORK/out/cmd" "$WORK/db/old" "$WORK/db/new" "$WORK/home"
DAY0=$(date +%F)

# ---- fixtures ---------------------------------------------------------------
echo "building fixtures in $WORK ..."
(cd "$ROOT" && go run ./tools/genfixtures --size small --seed 7 --out "$WORK/fx" --flat >/dev/null) || exit 2
EXPORT=$WORK/fx/export.xml

mkdb() { # name: an empty database with the current schema, made by the old binary
	"$OLD" --db "$WORK/db/$1.db" log weight 70 --at 2026-01-01T00:00:00Z >/dev/null || exit 2
	sqlite3 "$WORK/db/$1.db" "DELETE FROM samples"
}

"$OLD" --db "$WORK/db/main.db" import apple "$EXPORT" >/dev/null || exit 2
sqlite3 "$WORK/db/main.db" "
UPDATE samples SET start_ts = start_ts + 694*86400, end_ts = end_ts + 694*86400;
UPDATE sleep_stages SET start_ts = start_ts + 694*86400, end_ts = end_ts + 694*86400;
UPDATE workouts SET start_ts = start_ts + 694*86400, end_ts = end_ts + 694*86400;"
END_TS=$(sqlite3 "$WORK/db/main.db" "SELECT max(end_ts) FROM samples")
# The import time is the one real clock in the file; pin it to the data.
sqlite3 "$WORK/db/main.db" "
UPDATE meta SET value = '$END_TS' WHERE key = 'last_import_ts';
UPDATE meta SET value = json_set(value, '\$.at', $END_TS) WHERE key = 'last_import_summary';"
LAST_DAY=$(sqlite3 "$WORK/db/main.db" "SELECT date(max(end_ts), 'unixepoch') FROM samples")
MIDNIGHT=$((END_TS / 86400 * 86400))

# Sleep that is not sleep: five nights in bed only, the latest night awake only.
mkdb sleep
{
	echo "BEGIN;"
	for k in 0 1 2 3 4 5; do
		end=$((MIDNIGHT - k * 86400 + 23400)) # 06:30
		if [ $k -eq 0 ]; then
			echo "INSERT INTO sleep_stages (stage, start_ts, end_ts, tz_offset, source) VALUES ('awake', $((end - 1800)), $end, 0, 'Test Watch');"
		else
			echo "INSERT INTO sleep_stages (stage, start_ts, end_ts, tz_offset, source) VALUES ('in_bed', $((end - 28800)), $end, 0, 'Test Phone');"
		fi
		echo "INSERT INTO samples (metric, start_ts, end_ts, value, unit, tz_offset, source) VALUES ('step_count', $((MIDNIGHT - k * 86400 + 43200)), $((MIDNIGHT - k * 86400 + 43260)), $((5000 + k * 700)), 'count', 0, 'Test Phone');"
	done
	echo "COMMIT;"
} | sqlite3 "$WORK/db/sleep.db"

# One day only: a few readings, one night, one workout.
mkdb oneday
sqlite3 "$WORK/db/oneday.db" "
INSERT INTO samples (metric, start_ts, end_ts, value, unit, tz_offset, source) VALUES
 ('step_count', $((MIDNIGHT + 43200)), $((MIDNIGHT + 43260)), 8200, 'count', 0, 'Test Watch'),
 ('heart_rate', $((MIDNIGHT + 32400)), $((MIDNIGHT + 32460)), 71, 'bpm', 0, 'Test Watch'),
 ('resting_heart_rate', $((MIDNIGHT + 28800)), $((MIDNIGHT + 28860)), 55, 'bpm', 0, 'Test Watch'),
 ('body_mass', $((MIDNIGHT + 25200)), $((MIDNIGHT + 25200)), 72.4, 'kg', 0, 'manual');
INSERT INTO sleep_stages (stage, start_ts, end_ts, tz_offset, source) VALUES
 ('core', $((MIDNIGHT - 3600)), $((MIDNIGHT + 10800)), 0, 'Test Watch'),
 ('deep', $((MIDNIGHT + 10800)), $((MIDNIGHT + 14400)), 0, 'Test Watch'),
 ('rem', $((MIDNIGHT + 14400)), $((MIDNIGHT + 18000)), 0, 'Test Watch'),
 ('awake', $((MIDNIGHT + 18000)), $((MIDNIGHT + 18600)), 0, 'Test Watch');
INSERT INTO workouts (activity, start_ts, end_ts, duration_s, energy_kcal, distance_m, tz_offset, source) VALUES
 ('running', $((MIDNIGHT + 61200)), $((MIDNIGHT + 63900)), 2700, 410, 6100, 0, 'Test Watch');"

mkdb empty

for which in old new; do
	for name in main sleep oneday empty; do cp "$WORK/db/$name.db" "$WORK/db/$which/$name.db"; done
done

# ---- running one case -------------------------------------------------------
N=0
FAILED=0
: >"$WORK/differ.list"

# tidy: carriage returns and the ^D echo of the pseudo terminal, the work
# directory's name, and the one thing that differs between two runs of the same
# binary: elapsed time.
tidy() {
	tr -d '\r' | perl -pe 's/^\^D\x08\x08// if $. == 1; s/^Elapsed:.*/Elapsed: <time>/; s/"elapsed_seconds": *[0-9.eE+-]+/"elapsed_seconds": 0/; s/\Q$ENV{WORK}\E/<work>/g'
}

# exec_case <which> <tty 0|1> <columns> <extra env, one word or -> <db> <args...>
exec_case() {
	local which=$1 tty=$2 cols=$3 extra=$4 db=$5 bin
	shift 5
	[ "$which" = old ] && bin=$OLD || bin=$NEW
	[ "$extra" = - ] && extra=HC_NOOP=1
	local base=(env -i PATH="$PATH" HOME="$WORK/home" TERM=xterm-256color LANG=en_US.UTF-8 COLUMNS="$cols" "$extra")
	[ "$cols" = - ] && base=(env -i PATH="$PATH" HOME="$WORK/home" TERM=xterm-256color LANG=en_US.UTF-8 "$extra")
	local dbarg=()
	[ "$db" != - ] && dbarg=(--db "$WORK/db/$which/$db.db")
	if [ "$tty" = 1 ]; then
		script -q /dev/null "${base[@]}" sh -c '"$@"; echo "exit=$?"' sh "$bin" "${dbarg[@]}" "$@" </dev/null | tidy
	else
		"${base[@]}" sh -c '"$@" 2>&1; echo "exit=$?"' sh "$bin" "${dbarg[@]}" "$@" </dev/null | tidy
	fi
}

# case_run <label> <db> <tty> <columns> <extra env> <args...>
case_run() {
	local label=$1 db=$2 tty=$3 cols=$4 extra=$5
	shift 5
	N=$((N + 1))
	local id
	id=$(printf '%04d' "$N")
	echo "$label | db=$db tty=$tty COLUMNS=$cols env=$extra | healthcli $*" | sed "s|$WORK|<work>|g" >"$WORK/out/cmd/$id"
	exec_case old "$tty" "$cols" "$extra" "$db" "$@" >"$WORK/out/old/$id" 2>&1
	exec_case new "$tty" "$cols" "$extra" "$db" "$@" >"$WORK/out/new/$id" 2>&1
	if ! cmp -s "$WORK/out/old/$id" "$WORK/out/new/$id"; then
		FAILED=$((FAILED + 1))
		echo "$id" >>"$WORK/differ.list"
	fi
}

# ---- the matrix -------------------------------------------------------------
echo "running the matrix ..."
# Screens (overview, today, sleep, workouts, heatmap, trend) in every look.
screen_modes() { # db args...
	local db=$1 w
	shift
	for w in 50 60 80 100; do case_run "terminal $w" "$db" 1 "$w" - "$@"; done
	case_run "terminal ascii" "$db" 1 80 - "$@" --ascii
	case_run "terminal --no-color" "$db" 1 80 - "$@" --no-color
	case_run "terminal NO_COLOR" "$db" 1 80 NO_COLOR=1 "$@"
	case_run "piped" "$db" 0 - - "$@"
	case_run "piped 50" "$db" 0 50 - "$@"
	case_run "piped FORCE_COLOR" "$db" 0 80 FORCE_COLOR=1 "$@"
}
screen_modes_small() { # db args...: for the odd databases
	local db=$1
	shift
	case_run "terminal 50" "$db" 1 50 - "$@"
	case_run "terminal 80" "$db" 1 80 - "$@"
	case_run "piped" "$db" 0 - - "$@"
}
json_mode() { case_run "json" "$1" 0 - - "${@:2}" --json; }

# run_screens <db> <modes function>: every screen command in the list below.
run_screens() {
	local db=$1 mode=$2 words
	while read -ra words; do
		"$mode" "$db" "${words[@]}"
	done <<EOF
overview
overview --compact
overview --full
overview --category sleep,workouts
overview --weeks 4 --heatmap sleep
today
today --compact
today --category vitals
today --date $LAST_DAY
sleep
sleep --by bedtime
sleep --by efficiency --weeks 4
workouts
workouts --by type
workouts --days 2
heatmap
heatmap sleep
heatmap workouts
heatmap heart_rate --weeks 8
heatmap body_mass --weeks 8
trend steps --days 14
trend steps --days 14 -v
trend sleep --days 14
trend rhr --days 14
trend weight --days 14
trend steps --days 10 --date $LAST_DAY
EOF
}

run_screens main screen_modes
run_screens sleep screen_modes_small
run_screens oneday screen_modes_small
run_screens empty screen_modes_small

# JSON, where it exists.
for db in main sleep oneday empty; do
	for c in "overview" "overview --category vitals" "today" "today --date $LAST_DAY" "sleep" "sleep --by bedtime" \
		"workouts" "workouts --days 2" "heatmap" "heatmap sleep" "heatmap workouts" "trend steps --days 14" "trend sleep --days 14" "trend rhr --days 14"; do
		# shellcheck disable=SC2086
		json_mode "$db" $c
	done
done

# query, tables and JSON.
for db in main sleep oneday empty; do
	for q in "SELECT metric, unit, count(*) AS n, round(avg(value), 2) AS avg FROM samples GROUP BY metric ORDER BY metric" \
		"SELECT stage, count(*) AS n, sum(end_ts - start_ts) AS secs FROM sleep_stages GROUP BY stage ORDER BY stage" \
		"SELECT activity, count(*) AS n, round(sum(distance_m)) AS m FROM workouts GROUP BY activity" \
		"SELECT * FROM meta ORDER BY key" "SELECT 1; SELECT 2" "DELETE FROM samples" "SELEC 1"; do
		case_run "query" "$db" 0 - - query "$q"
		case_run "query json" "$db" 0 - - query "$q" --json
	done
done

# Help of everything, errors and small commands (no database needed).
for c in "" "help" "version" "completion" "import" "import apple" "log" "query" "overview" "today" "sleep" "workouts" "heatmap" "trend" \
	"completion bash" "completion zsh" "completion fish" "completion powershell"; do
	# shellcheck disable=SC2086
	case_run "help" - 0 - - $c --help
done
case_run "no arguments" - 0 - -
case_run "version" - 0 - - version
case_run "version json" - 0 - - version --json
for shell in bash zsh fish powershell; do case_run "completion" - 0 - - completion "$shell"; done
case_run "unknown command" - 0 - - bogus
case_run "unknown flag" main 0 - - trend steps --bogus
case_run "missing database" - 0 - - --db "$WORK/db/none.db" overview
case_run "bad date" main 0 - - today --date 31/12/2026
case_run "unknown metric" main 0 - - trend bogus
case_run "dynamic completion" main 0 - - __complete trend ""

# log writes: the same lines into each binary's copy, then read back.
for db in main empty; do
	case_run "log" "$db" 0 - - log weight 72.5 --at 2026-11-01T08:00:00Z
	case_run "log again" "$db" 0 - - log weight 72.5 --at 2026-11-01T08:00:00Z
	case_run "log lb json" "$db" 0 - - log weight 160 --unit lb --at 2026-11-02T08:00:00Z --json
	case_run "log refused" "$db" 0 - - log weight abc
	case_run "log read back" "$db" 0 - - query "SELECT metric, value, unit, source, start_ts FROM samples WHERE source = 'manual' ORDER BY start_ts"
	case_run "overview after log" "$db" 1 80 - overview
done

# import: the summary, and every row it saved.
for which in old new; do rm -f "$WORK/db/$which/import.db" "$WORK/db/$which/import-again.db"; done
case_run "import" import 0 - - import apple "$EXPORT"
case_run "import again" import 0 - - import apple "$EXPORT"
case_run "import json" import-again 0 - - import apple "$EXPORT" --json
case_run "import missing file" import-again 0 - - import apple "$WORK/fx/missing.zip"
for which in old new; do
	sqlite3 "$WORK/db/$which/import.db" "
	SELECT 'samples', metric, start_ts, end_ts, value, unit, tz_offset, source FROM samples ORDER BY metric, start_ts, end_ts, source;
	SELECT 'sleep', stage, start_ts, end_ts, tz_offset, source FROM sleep_stages ORDER BY start_ts, end_ts, stage, source;
	SELECT 'workouts', activity, start_ts, end_ts, duration_s, energy_kcal, distance_m, tz_offset, source FROM workouts ORDER BY start_ts, activity, source;" >"$WORK/out/$which/import-rows"
done
N=$((N + 1))
echo "imported rows, every table | db=import | sqlite3" >"$WORK/out/cmd/$(printf '%04d' $N)"
cp "$WORK/out/old/import-rows" "$WORK/out/old/$(printf '%04d' $N)"
cp "$WORK/out/new/import-rows" "$WORK/out/new/$(printf '%04d' $N)"
if ! cmp -s "$WORK/out/old/import-rows" "$WORK/out/new/import-rows"; then
	FAILED=$((FAILED + 1))
	printf '%04d\n' $N >>"$WORK/differ.list"
fi

# ---- the verdict ------------------------------------------------------------
if [ "$(date +%F)" != "$DAY0" ]; then
	echo "the date changed while the script ran ($DAY0 -> $(date +%F)); run it again" >&2
	exit 2
fi
echo "$N cases, $FAILED differ (outputs in $WORK/out/{old,new}, command lines in $WORK/out/cmd)"
if [ "$FAILED" -gt 0 ]; then
	echo
	head -5 "$WORK/differ.list" | while read -r id; do
		echo "=== case $id: $(cat "$WORK/out/cmd/$id")"
		diff -u "$WORK/out/old/$id" "$WORK/out/new/$id" | head -20
		echo
	done
	[ "$FAILED" -gt 5 ] && echo "... and $((FAILED - 5)) more: $WORK/differ.list"
	exit 1
fi
exit 0
