package stats

import (
	"context"
	"database/sql"
	"fmt"
	"sort"
	"time"
)

// SleepNight is the sleep of one night.
type SleepNight struct {
	// Night is the local date the sleep ENDS on: Sunday 23:00 to Monday
	// 07:00 is Monday's night, which is what "how did I sleep today" means.
	// A sleep day runs from 18:00 to 18:00 local time: a stage row that
	// ends at or after 18:00 already counts for the NEXT date (see
	// nightShift).
	Night string

	// Source is the one source the stage totals below come from.
	Source string
	Rows   int // how many stage rows of Source were used

	// Start and End are the first and last moment of ASLEEP time (Unix
	// seconds), 0 when the night has no asleep time. TZ is the UTC offset in
	// seconds the night was recorded in, so Start and End can be shown as the
	// clock on the wall.
	Start, End int64
	TZ         int

	// InBedStart and InBedEnd are the first and last in_bed moments (0 = none).
	InBedStart, InBedEnd int64

	// Awakenings counts awake periods of 5 minutes or more between Start and
	// End; shorter blips are ignored. Latency is how long falling asleep took
	// (first asleep moment minus first in_bed moment), 0 when unknown: no
	// in_bed row, or a gap over 90 minutes (the iPhone often starts "in bed"
	// long before the Watch notices sleep, so it is only a guess).
	Awakenings int
	Latency    time.Duration

	// Segments are the stage rows of Source (without in_bed), oldest first,
	// for drawing the hypnogram.
	Segments []Seg

	// Asleep is the UNION of the core, deep, REM and unspecified intervals
	// (so two stage rows that overlap count once). Awake and InBed are not sleep.
	Asleep      time.Duration
	Core        time.Duration
	Deep        time.Duration
	REM         time.Duration
	Unspecified time.Duration // "asleep_unspecified": older iOS, other apps
	Awake       time.Duration

	// InBed is time in bed, often written by the iPhone while the Watch writes
	// the stages. It is picked separately (same priority rule) so a Watch
	// night does not lose the iPhone's in-bed time. InBedSource is "" when
	// nobody recorded it.
	InBed       time.Duration
	InBedSource string
}

// nightShift moves a stage row's end time forward 6 hours before taking
// its date, so that 18:00..23:59 lands on the next day.
//
// Why: the Watch writes one row per stage. The first rows of Sunday night
// (say core sleep 23:12-23:59) end on Sunday, the rest on Monday. Taking
// each row's own end date would split one night across two days. With the
// shift every row of that night is Monday's. The cost: an afternoon nap
// that ends before 18:00 counts toward the night that just ended.
const nightShift = 6 * 60 * 60

// Seg is one stage row of a night: Stage is core, deep, rem, awake or
// asleep_unspecified; Start and End are Unix seconds.
type Seg struct {
	Stage      string
	Start, End int64
}

// interval is a stage row's [start, end) in Unix seconds.
type interval struct{ start, end int64 }

// merge sorts the intervals and joins the ones that overlap or touch. Rows
// that end before they start are dropped.
func merge(ivs []interval) []interval {
	ivs = append([]interval(nil), ivs...)
	sort.Slice(ivs, func(i, j int) bool { return ivs[i].start < ivs[j].start })
	var out []interval
	for _, iv := range ivs {
		if iv.end <= iv.start {
			continue
		}
		if n := len(out); n > 0 && iv.start <= out[n-1].end {
			out[n-1].end = max(out[n-1].end, iv.end)
			continue
		}
		out = append(out, iv)
	}
	return out
}

// length is the total seconds of already merged intervals.
func length(merged []interval) time.Duration {
	var secs int64
	for _, iv := range merged {
		secs += iv.end - iv.start
	}
	return time.Duration(secs) * time.Second
}

// sleepTotals collects the stage rows of one source for one night.
type sleepTotals struct {
	rows    int
	tz      int // tz_offset of the first row read
	byStage map[string][]interval
}

// total is how long the stage lasted. Rows of the same stage that overlap
// (a revised estimate written next to the old one) count once, and a row
// that ends before it starts counts for nothing.
func (t *sleepTotals) total(stage string) time.Duration {
	return length(merge(t.byStage[stage]))
}

// asleepUnion joins the intervals of every asleep stage. Summing the stages
// one by one would count twice a moment that two stages both claim.
func (t *sleepTotals) asleepUnion() []interval {
	var all []interval
	for _, stage := range asleepStages {
		all = append(all, t.byStage[stage]...)
	}
	return merge(all)
}

var asleepStages = []string{"core", "deep", "rem", "asleep_unspecified"}

// SleepNights returns one SleepNight per night from fromDay to toDay (both
// inclusive, by the local date the sleep ends, see SleepNight.Night),
// sorted by night. Nights without any rows are left out.
//
// Source rule: per night, the stage totals (everything except in_bed) come
// from the single best source that has stage rows that night; in_bed comes
// from the single best source that has in_bed rows that night.
func SleepNights(ctx context.Context, conn *sql.DB, fromDay, toDay string) ([]SleepNight, error) {
	lo, hi, err := utcWindow(fromDay, toDay)
	if err != nil {
		return nil, err
	}

	// The window is for the SHIFTED end time, so move it back by the shift
	// to filter on plain end_ts, which uses the sleep_stages_end_ts index
	// (migration 0002). tz_offset is the one the row STARTED in, which is
	// all the database stores.
	rows, err := conn.QueryContext(ctx, `
		SELECT date(end_ts + tz_offset + ?, 'unixepoch') AS night, stage, source, start_ts, end_ts, tz_offset
		FROM sleep_stages
		WHERE end_ts >= ? AND end_ts < ?
		  AND date(end_ts + tz_offset + ?, 'unixepoch') BETWEEN ? AND ?`,
		nightShift, lo-nightShift, hi-nightShift, nightShift, fromDay, toDay)
	if err != nil {
		return nil, fmt.Errorf("stats: read sleep: %w", err)
	}
	defer rows.Close()

	// night -> source -> totals, kept apart for stages and for in_bed.
	stages := map[string]map[string]*sleepTotals{}
	inBed := map[string]map[string]*sleepTotals{}
	for rows.Next() {
		var night, stage, source string
		var start, end int64
		var tz int
		if err := rows.Scan(&night, &stage, &source, &start, &end, &tz); err != nil {
			return nil, fmt.Errorf("stats: read sleep: %w", err)
		}
		group := stages
		if stage == "in_bed" {
			group = inBed
		}
		if group[night] == nil {
			group[night] = map[string]*sleepTotals{}
		}
		t := group[night][source]
		if t == nil {
			t = &sleepTotals{tz: tz, byStage: map[string][]interval{}}
			group[night][source] = t
		}
		t.rows++
		t.byStage[stage] = append(t.byStage[stage], interval{start, end})
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("stats: read sleep: %w", err)
	}

	// Every night that has stage rows, in_bed rows, or both.
	var nights []string
	for night := range stages {
		nights = append(nights, night)
	}
	for night := range inBed {
		if stages[night] == nil {
			nights = append(nights, night)
		}
	}
	sort.Strings(nights)

	out := make([]SleepNight, 0, len(nights))
	for _, night := range nights {
		n := SleepNight{Night: night}
		if source, t := bestSleepSource(stages[night]); t != nil {
			n.Source = source
			n.Rows = t.rows
			n.TZ = t.tz
			n.Core = t.total("core")
			n.Deep = t.total("deep")
			n.REM = t.total("rem")
			n.Unspecified = t.total("asleep_unspecified")
			n.Awake = t.total("awake")
			fillAsleepTimes(&n, t)
		}
		if source, t := bestSleepSource(inBed[night]); t != nil {
			n.InBedSource = source
			n.InBed = t.total("in_bed")
			if m := merge(t.byStage["in_bed"]); len(m) > 0 {
				n.InBedStart, n.InBedEnd = m[0].start, m[len(m)-1].end
			}
			if n.TZ == 0 {
				n.TZ = t.tz
			}
		}
		// Falling-asleep time only makes sense when in bed began first, and
		// only up to 90 minutes: longer means the two sources disagree.
		if lat := time.Duration(n.Start-n.InBedStart) * time.Second; n.InBedStart > 0 && n.Start > 0 && lat > 0 && lat <= 90*time.Minute {
			n.Latency = lat
		}
		out = append(out, n)
	}
	return out, nil
}

// bestSleepSource picks the preferred source of one night. It returns a nil
// totals when the map is empty (nil maps are fine to range over in Go).
func bestSleepSource(bySource map[string]*sleepTotals) (string, *sleepTotals) {
	best := ""
	var bt *sleepTotals
	for source, t := range bySource {
		if bt == nil || better(source, t.rows, best, bt.rows) {
			best, bt = source, t
		}
	}
	return best, bt
}

// minAwakening is the shortest awake period that counts as an awakening.
const minAwakening = 5 * time.Minute

// fillAsleepTimes sets Asleep, Start, End, Awakenings and Segments of n from
// the stage rows of one source.
func fillAsleepTimes(n *SleepNight, t *sleepTotals) {
	asleep := t.asleepUnion()
	n.Asleep = length(asleep)
	if len(asleep) > 0 {
		n.Start, n.End = asleep[0].start, asleep[len(asleep)-1].end
	}
	for _, iv := range merge(t.byStage["awake"]) {
		if iv.start >= n.Start && iv.end <= n.End && time.Duration(iv.end-iv.start)*time.Second >= minAwakening {
			n.Awakenings++
		}
	}
	for stage, ivs := range t.byStage {
		if stage == "in_bed" {
			continue
		}
		for _, iv := range ivs {
			if iv.end > iv.start {
				n.Segments = append(n.Segments, Seg{Stage: stage, Start: iv.start, End: iv.end})
			}
		}
	}
	// Sort by time; the stage name breaks ties so the order never depends on
	// map iteration.
	sort.Slice(n.Segments, func(i, j int) bool {
		a, b := n.Segments[i], n.Segments[j]
		if a.Start != b.Start {
			return a.Start < b.Start
		}
		if a.End != b.End {
			return a.End < b.End
		}
		return a.Stage < b.Stage
	})
}
