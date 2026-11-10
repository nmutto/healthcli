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

	// Start and End are the first and last moment of the MAIN sleep block
	// (Unix seconds), 0 when the night has no asleep time. Naps are not part
	// of it, see mainBlock. TZ is the UTC offset in seconds of the first row;
	// StartTZ and EndTZ are the offsets of the rows Start and End belong to
	// (they differ when the clocks changed during the night), so each can be
	// shown as the clock on the wall it was recorded on.
	Start, End     int64
	TZ             int
	StartTZ, EndTZ int

	// InBedStart and InBedEnd are the first and last in_bed moments (0 = none),
	// with the offsets of their rows.
	InBedStart, InBedEnd     int64
	InBedStartTZ, InBedEndTZ int

	// Awakenings counts every awake period between Start and End. The Watch
	// writes short ones (a minute or two), and they are what Apple Health
	// shows too, so there is no minimum length. Latency is how long falling asleep took
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
	//
	// Asleep includes naps; Nap is the part of it outside the main block.
	Asleep      time.Duration
	Nap         time.Duration
	Core        time.Duration
	Deep        time.Duration
	REM         time.Duration
	Unspecified time.Duration // "asleep_unspecified": older iOS, other apps
	Awake       time.Duration // awake time inside the main block only

	// InBed is time in bed, often written by the iPhone while the Watch writes
	// the stages. It is picked separately (same priority rule) so a Watch
	// night does not lose the iPhone's in-bed time. InBedSource is "" when
	// nobody recorded it.
	InBed       time.Duration
	InBedSource string

	// hadAwakeRows is true when the source wrote any awake row that night,
	// also outside the main block. Efficiency without in_bed rows needs it:
	// no awake rows means "not measured", not "never awake".
	hadAwakeRows bool
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
// asleep_unspecified; Start and End are Unix seconds; TZ is the row's offset.
type Seg struct {
	Stage      string
	Start, End int64
	TZ         int
}

// interval is a stage row's [start, end) in Unix seconds. startTZ is the UTC
// offset of the row that starts it, endTZ of the row that ends it (after a
// merge these can be different rows).
type interval struct {
	start, end     int64
	startTZ, endTZ int
}

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
			if iv.end > out[n-1].end {
				out[n-1].end, out[n-1].endTZ = iv.end, iv.endTZ
			}
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

// awake is the merged awake rows of the source. Awake wins over an asleep row
// that overlaps it, so asleep stages are counted without these moments.
func (t *sleepTotals) awake() []interval { return merge(t.byStage["awake"]) }

// total is how long the stage lasted. Rows of the same stage that overlap
// (a revised estimate written next to the old one) count once, and a row
// that ends before it starts counts for nothing. Asleep stages lose the
// moments the source also calls awake.
func (t *sleepTotals) total(stage string) time.Duration {
	merged := merge(t.byStage[stage])
	if stage != "awake" && stage != "in_bed" {
		merged = subtract(merged, t.awake())
	}
	return length(merged)
}

// asleepUnion joins the intervals of every asleep stage, minus the awake
// moments. Summing the stages one by one would count twice a moment that two
// stages both claim.
func (t *sleepTotals) asleepUnion() []interval {
	var all []interval
	for _, stage := range asleepStages {
		all = append(all, t.byStage[stage]...)
	}
	return subtract(merge(all), t.awake())
}

// subtract removes the moments of cut from the merged intervals. Both lists
// must be merged (sorted, no overlaps).
func subtract(merged, cut []interval) []interval {
	var out []interval
	for _, iv := range merged {
		start, startTZ := iv.start, iv.startTZ
		for _, c := range cut {
			if c.end <= start || c.start >= iv.end {
				continue
			}
			if c.start > start {
				out = append(out, interval{start, c.start, startTZ, c.startTZ})
			}
			if c.end > start {
				start, startTZ = c.end, c.endTZ
			}
		}
		if start < iv.end {
			out = append(out, interval{start, iv.end, startTZ, iv.endTZ})
		}
	}
	return out
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
		t.byStage[stage] = append(t.byStage[stage], interval{start, end, tz, tz})
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
			n.TZ, n.StartTZ, n.EndTZ = t.tz, t.tz, t.tz
			n.Core = t.total("core")
			n.Deep = t.total("deep")
			n.REM = t.total("rem")
			n.Unspecified = t.total("asleep_unspecified")
			fillAsleepTimes(&n, t)
			addOtherSourceNaps(&n, t, stages[night])
		}
		if source, t := bestSleepSource(inBed[night]); t != nil {
			n.InBedSource = source
			n.InBed = t.total("in_bed")
			if m := merge(t.byStage["in_bed"]); len(m) > 0 {
				n.InBedStart, n.InBedEnd = m[0].start, m[len(m)-1].end
				n.InBedStartTZ, n.InBedEndTZ = m[0].startTZ, m[len(m)-1].endTZ
			}
			if n.TZ == 0 {
				n.TZ, n.StartTZ, n.EndTZ = t.tz, t.tz, t.tz
			}
		}
		// Awake rows alone are no sleep and no time in bed: not a night.
		if n.Asleep == 0 && n.InBed == 0 {
			continue
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
//
// A source only counts when it has asleep time (a Watch that wrote one awake
// row must not beat an iPhone that has the whole night) and when its main
// sleep block is near the longest main block of all sources (a Watch that
// only caught an afternoon nap did not see the night). Among those the
// existing class priority decides, then more asleep time, then row count.
// If no source has asleep time (in_bed rows, awake-only rows) the old rule
// applies to all of them.
func bestSleepSource(bySource map[string]*sleepTotals) (string, *sleepTotals) {
	// The longest main block of any source is "the night".
	var night block
	for _, t := range bySource {
		if m, ok := t.mainSleepBlock(); ok && m.asleep > night.asleep {
			night = m
		}
	}

	best := ""
	var bt *sleepTotals
	for source, t := range bySource {
		if bt == nil || preferSleepSource(source, t, best, bt, night) {
			best, bt = source, t
		}
	}
	return best, bt
}

// preferSleepSource reports whether source a (totals ta) beats source b (tb)
// for a night whose main block is night (zero when nobody has asleep time).
func preferSleepSource(a string, ta *sleepTotals, b string, tb *sleepTotals, night block) bool {
	seesA, seesB := ta.seesNight(night), tb.seesNight(night)
	if seesA != seesB {
		return seesA
	}
	if SourcePriority(a) == SourcePriority(b) {
		if da, db := length(ta.asleepUnion()), length(tb.asleepUnion()); da != db {
			return da > db
		}
	}
	return better(a, ta.rows, b, tb.rows)
}

// mainSleepBlock is the main block of the source's own asleep time; ok is
// false when the source has none.
func (t *sleepTotals) mainSleepBlock() (block, bool) {
	blocks := sleepBlocks(t.asleepUnion())
	if len(blocks) == 0 {
		return block{}, false
	}
	return mainBlock(blocks), true
}

// seesNight reports whether the source has asleep time whose main block is
// within napGap of night. With no night (nobody has asleep time) every
// source qualifies, so the usual priority picks one.
func (t *sleepTotals) seesNight(night block) bool {
	if night.asleep == 0 {
		return true
	}
	m, ok := t.mainSleepBlock()
	if !ok {
		return false
	}
	return time.Duration(max(m.start-night.end, night.start-m.end))*time.Second <= napGap
}

// napGap is how far apart two pieces of sleep must be to be separate blocks.
const napGap = 3 * time.Hour

// block is a run of asleep intervals with gaps of at most napGap between them.
type block struct {
	start, end     int64
	startTZ, endTZ int
	asleep         time.Duration // asleep time inside the block
}

// sleepBlocks groups merged asleep intervals into blocks.
func sleepBlocks(asleep []interval) []block {
	var out []block
	for _, iv := range asleep {
		if n := len(out); n > 0 && time.Duration(iv.start-out[n-1].end)*time.Second <= napGap {
			out[n-1].end, out[n-1].endTZ = iv.end, iv.endTZ
			out[n-1].asleep += time.Duration(iv.end-iv.start) * time.Second
			continue
		}
		out = append(out, block{iv.start, iv.end, iv.startTZ, iv.endTZ, time.Duration(iv.end-iv.start) * time.Second})
	}
	return out
}

// Nap rule. The MAIN sleep block of a night is the block of asleep time with
// the most sleep in it (the first one on a tie); asleep pieces more than 3
// hours away from it are naps. Bedtime, wake, midpoint, awakenings and the
// hypnogram use the main block only, so a nap cannot stretch the night or move
// its midpoint. Naps stay in Asleep (total time asleep) and are also reported
// as Nap. A nap written by another source than the night's best source is not
// dropped: see addOtherSourceNaps.
func mainBlock(blocks []block) block {
	best := blocks[0]
	for _, b := range blocks[1:] {
		if b.asleep > best.asleep {
			best = b
		}
	}
	return best
}

// fillAsleepTimes sets Asleep, Nap, Start, End, Awake, Awakenings and Segments of n
// from the stage rows of one source.
func fillAsleepTimes(n *SleepNight, t *sleepTotals) {
	asleep := t.asleepUnion()
	n.Asleep = length(asleep)
	if len(asleep) > 0 {
		main := mainBlock(sleepBlocks(asleep))
		n.Start, n.End, n.StartTZ, n.EndTZ = main.start, main.end, main.startTZ, main.endTZ
		n.Nap = n.Asleep - main.asleep
	}
	// An awakening is an awake period inside the main block, and Awake is
	// their total time. Awake before falling asleep is latency and awake
	// after the last wake is just being up, so awake rows are clipped to
	// Start..End.
	n.hadAwakeRows = len(t.byStage["awake"]) > 0
	if len(asleep) > 0 {
		var inside []interval
		for _, iv := range merge(t.byStage["awake"]) {
			iv.start, iv.end = max(iv.start, n.Start), min(iv.end, n.End)
			if iv.end > iv.start {
				inside = append(inside, iv)
			}
		}
		n.Awakenings = len(inside)
		n.Awake = length(inside)
	}
	for stage, ivs := range t.byStage {
		if stage == "in_bed" {
			continue
		}
		for _, iv := range ivs {
			// Only rows that touch the main block are drawn (no nap rows).
			if iv.end > iv.start && iv.end > n.Start && iv.start < n.End {
				n.Segments = append(n.Segments, Seg{Stage: stage, Start: iv.start, End: iv.end, TZ: iv.startTZ})
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

// addOtherSourceNaps adds sleep that only another source recorded and that is
// a nap by the rule above (more than 3 hours from every asleep interval of the
// night's best source). The best-source rule keeps one source per night for
// the stage numbers, which would silently lose such a nap; here it counts
// toward Asleep and Nap only (no stage totals). Asleep time of other sources
// near the best source's sleep is the same sleep seen twice and stays ignored.
func addOtherSourceNaps(n *SleepNight, best *sleepTotals, bySource map[string]*sleepTotals) {
	own := best.asleepUnion()
	var extra []interval
	for _, t := range bySource {
		if t == best {
			continue
		}
		for _, iv := range t.asleepUnion() {
			far := true
			for _, o := range own {
				if time.Duration(max(iv.start-o.end, o.start-iv.end))*time.Second <= napGap {
					far = false
					break
				}
			}
			if far {
				extra = append(extra, iv)
			}
		}
	}
	d := length(merge(extra))
	n.Asleep += d
	n.Nap += d
}
