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
	Source   string
	Segments int // how many stage rows of Source were used

	// Asleep = Core + Deep + REM + Unspecified. Awake and InBed are not sleep.
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

// interval is a stage row's [start, end) in Unix seconds.
type interval struct{ start, end int64 }

// sleepTotals collects the stage rows of one source for one night.
type sleepTotals struct {
	rows    int
	byStage map[string][]interval
}

// total is how long the stage lasted. Rows of the same stage that overlap
// (a revised estimate written next to the old one) count once, and a row
// that ends before it starts counts for nothing.
func (t *sleepTotals) total(stage string) time.Duration {
	ivs := append([]interval(nil), t.byStage[stage]...)
	sort.Slice(ivs, func(i, j int) bool { return ivs[i].start < ivs[j].start })
	var secs, curEnd int64
	started := false
	for _, iv := range ivs {
		if iv.end <= iv.start {
			continue
		}
		if !started || iv.start > curEnd {
			secs += iv.end - iv.start
			curEnd, started = iv.end, true
		} else if iv.end > curEnd {
			secs += iv.end - curEnd
			curEnd = iv.end
		}
	}
	return time.Duration(secs) * time.Second
}

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
		SELECT date(end_ts + tz_offset + ?, 'unixepoch') AS night, stage, source, start_ts, end_ts
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
		if err := rows.Scan(&night, &stage, &source, &start, &end); err != nil {
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
			t = &sleepTotals{byStage: map[string][]interval{}}
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
			n.Segments = t.rows
			n.Core = t.total("core")
			n.Deep = t.total("deep")
			n.REM = t.total("rem")
			n.Unspecified = t.total("asleep_unspecified")
			n.Awake = t.total("awake")
			n.Asleep = n.Core + n.Deep + n.REM + n.Unspecified
		}
		if source, t := bestSleepSource(inBed[night]); t != nil {
			n.InBedSource = source
			n.InBed = t.total("in_bed")
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
