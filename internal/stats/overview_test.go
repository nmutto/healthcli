package stats

import (
	"context"
	"fmt"
	"slices"
	"testing"
	"time"

	"github.com/nmutto/healthcli/internal/importer"
	"github.com/nmutto/healthcli/internal/metrics"
)

var overviewNow = time.Date(2026, 11, 8, 15, 0, 0, 0, time.UTC)

func rowFor(t *testing.T, o *Overview, metric string) MetricRow {
	t.Helper()
	for _, r := range o.Metrics {
		if r.Metric == metric {
			return r
		}
	}
	t.Fatalf("no row for %s", metric)
	return MetricRow{}
}

func TestOverviewRowStates(t *testing.T) {
	conn := newDB(t)
	// steps: 10 days of history plus a big fresh day -> ok
	for i := 1; i <= 10; i++ {
		addSample(t, conn, "step_count", fmt.Sprintf("2026-10-%02d 12:00 +0000", 20+i-1), 6000+float64(i)*100, watch)
	}
	addSample(t, conn, "step_count", "2026-11-08 12:00 +0000", 20000, watch)
	// resting HR: only 3 days -> no_baseline
	for _, d := range []string{"06", "07", "08"} {
		addSample(t, conn, "resting_heart_rate", "2026-11-"+d+" 07:00 +0000", 58, watch)
	}
	// weight: one reading 9 days ago -> stale
	addSample(t, conn, "body_mass", "2026-10-30 08:00 +0000", 72.4, "manual")
	// hrv: nothing -> not shown at all, listed in NotTracked

	o, err := BuildOverview(context.Background(), conn, overviewNow, DefaultOverviewOptions())
	if err != nil {
		t.Fatal(err)
	}
	cases := []struct {
		metric, state string
		age           int
	}{
		{"step_count", StateOK, 0},
		{"resting_heart_rate", StateNoBaseline, 0},
		{"body_mass", StateStale, 9},
	}
	for _, c := range cases {
		r := rowFor(t, o, c.metric)
		if r.State != c.state || r.AgeDays != c.age {
			t.Errorf("%s: state %s age %d, want %s age %d", c.metric, r.State, r.AgeDays, c.state, c.age)
		}
	}
	for _, r := range o.Metrics {
		if r.State == StateNoData {
			t.Errorf("%s: a no_data row must not be listed", r.Metric)
		}
	}
	if !slices.Contains(o.NotTracked, "hrv_sdnn") || slices.Contains(o.NotTracked, "step_count") {
		t.Errorf("not_tracked = %v", o.NotTracked)
	}
	if s := rowFor(t, o, "step_count"); !s.Differs() || *s.Comparison != Above {
		t.Errorf("steps should be above usual: %+v", s)
	}
	if w := rowFor(t, o, "body_mass"); w.Value == nil || w.Comparison != nil {
		t.Errorf("stale weight keeps its value but has no comparison: %+v", w)
	}
	if o.Empty {
		t.Error("database with samples must not be Empty")
	}
	if len(rowFor(t, o, "step_count").Spark) != SparkDays {
		t.Errorf("spark length %d, want %d", len(rowFor(t, o, "step_count").Spark), SparkDays)
	}
}

func TestOverviewEmptyDatabase(t *testing.T) {
	o, err := BuildOverview(context.Background(), newDB(t), overviewNow, DefaultOverviewOptions())
	if err != nil {
		t.Fatal(err)
	}
	if !o.Empty || o.Sleep.State != StateNoData || o.Data.LastImport != nil {
		t.Errorf("unexpected: %+v", o)
	}
	if len(o.Metrics) != 0 || len(o.NotTracked) != len(metrics.Names()) || o.Heatmap != nil {
		t.Errorf("empty database: %d rows, %d not tracked, heatmap %v", len(o.Metrics), len(o.NotTracked), o.Heatmap)
	}
}

// The verdict is the comparison seen through the metric's polarity.
func TestVerdict(t *testing.T) {
	cases := []struct {
		p          metrics.Polarity
		comparison string
		want       string
	}{
		{metrics.HigherBetter, Above, VerdictBetter},
		{metrics.HigherBetter, Below, VerdictWorse},
		{metrics.HigherBetter, Typical, VerdictTypical},
		{metrics.LowerBetter, Below, VerdictBetter},
		{metrics.LowerBetter, Above, VerdictWorse},
		{metrics.LowerBetter, Typical, VerdictTypical},
		{metrics.Neutral, Above, VerdictChanged},
		{metrics.Neutral, Below, VerdictChanged},
		{metrics.Neutral, Typical, VerdictTypical},
	}
	for _, c := range cases {
		if got := Verdict(c.p, c.comparison); got != c.want {
			t.Errorf("Verdict(%s, %s) = %s, want %s", c.p, c.comparison, got, c.want)
		}
	}
}

// Rows come in category order, registry order inside a category, and the
// heatmap picks steps, then exercise time, then active energy.
func TestOverviewOrderAndAutoHeatmap(t *testing.T) {
	conn := newDB(t)
	addSample(t, conn, "body_mass", "2026-11-08 08:00 +0000", 72, "manual")
	addSample(t, conn, "active_energy", "2026-11-08 12:00 +0000", 500, watch)
	addSample(t, conn, "heart_rate", "2026-11-08 09:00 +0000", 66, watch)
	o, err := BuildOverview(context.Background(), conn, overviewNow, DefaultOverviewOptions())
	if err != nil {
		t.Fatal(err)
	}
	var got []string
	for _, r := range o.Metrics {
		got = append(got, r.Metric)
	}
	if want := []string{"heart_rate", "active_energy", "body_mass"}; !slices.Equal(got, want) {
		t.Errorf("order = %v, want %v", got, want)
	}
	if o.Heatmap == nil || o.Heatmap.Metric != "active_energy" {
		t.Errorf("heatmap = %+v, want active_energy", o.Heatmap)
	}
	opts := DefaultOverviewOptions()
	opts.HeatWeeks = 0
	if o, _ = BuildOverview(context.Background(), conn, overviewNow, opts); o.Heatmap != nil {
		t.Error("0 weeks must hide the heatmap")
	}
	addSample(t, conn, "step_count", "2026-11-08 12:00 +0000", 9000, watch)
	if o, _ = BuildOverview(context.Background(), conn, overviewNow, DefaultOverviewOptions()); o.Heatmap == nil || o.Heatmap.Metric != "step_count" {
		t.Errorf("steps should win: %+v", o.Heatmap)
	}
	opts = OverviewOptions{HeatWeeks: 4, HeatMetric: "heart_rate"}
	if o, _ = BuildOverview(context.Background(), conn, overviewNow, opts); o.Heatmap == nil || o.Heatmap.Metric != "heart_rate" || len(o.Heatmap.Weeks) != 4 {
		t.Errorf("override ignored: %+v", o.Heatmap)
	}
}

func TestOverviewGapsAndSources(t *testing.T) {
	conn := newDB(t)
	// Days 1,2,5 of November: 2 missing days inside a span of 5.
	for _, d := range []string{"01", "02", "05"} {
		addSample(t, conn, "hrv_sdnn", "2026-11-"+d+" 07:00 +0000", 60, watch)
	}
	addSample(t, conn, "step_count", "2026-11-05 07:00 +0000", 100, iphone)
	o, err := BuildOverview(context.Background(), conn, overviewNow, DefaultOverviewOptions())
	if err != nil {
		t.Fatal(err)
	}
	if len(o.Data.Gaps) != 1 || o.Data.Gaps[0].Missing != 2 || o.Data.Gaps[0].Span != 5 {
		t.Errorf("gaps = %+v, want hrv 2 of 5", o.Data.Gaps)
	}
	if o.Data.SourceCount[ClassWatch] != 3 || o.Data.SourceCount[ClassIPhone] != 1 {
		t.Errorf("sources = %v", o.Data.SourceCount)
	}
	if o.Data.FirstDay != "2026-11-01" || o.Data.LastDay != "2026-11-05" {
		t.Errorf("range = %s..%s", o.Data.FirstDay, o.Data.LastDay)
	}
}

func TestOverviewSleepOnly(t *testing.T) {
	conn := newDB(t)
	addSleep(t, conn, "core", "2026-11-07 23:00 +0000", "2026-11-08 05:00 +0000", watch)
	addSleep(t, conn, "in_bed", "2026-11-07 22:50 +0000", "2026-11-08 06:00 +0000", iphone)
	o, err := BuildOverview(context.Background(), conn, overviewNow, DefaultOverviewOptions())
	if err != nil {
		t.Fatal(err)
	}
	if o.Empty {
		t.Error("sleep-only database is not empty")
	}
	if o.Sleep.State != StateOK || o.Sleep.Night != "2026-11-08" || o.Sleep.AsleepSecs != 6*3600 || o.Sleep.InBedSecs != 7*3600+600 {
		t.Errorf("sleep = %+v", o.Sleep)
	}
}

func TestOverviewLastImportSummary(t *testing.T) {
	conn := newDB(t)
	if _, err := importer.Import(conn, "../../testdata/small/export.xml", importer.Options{}); err != nil {
		t.Fatal(err)
	}
	o, err := BuildOverview(context.Background(), conn, overviewNow, DefaultOverviewOptions())
	if err != nil {
		t.Fatal(err)
	}
	li := o.Data.LastImport
	if li == nil || li.Inserted == 0 || li.Read < li.Inserted || li.At == 0 {
		t.Fatalf("last import = %+v", li)
	}
	// Importing again: everything is a duplicate, and the summary reflects the LAST run.
	if _, err := importer.Import(conn, "../../testdata/small/export.xml", importer.Options{}); err != nil {
		t.Fatal(err)
	}
	o, _ = BuildOverview(context.Background(), conn, overviewNow, DefaultOverviewOptions())
	if o.Data.LastImport.Inserted != 0 || o.Data.LastImport.Duplicates < li.Inserted {
		t.Errorf("second import = %+v, first inserted %d", o.Data.LastImport, li.Inserted)
	}
}

func TestAgeOf(t *testing.T) {
	cases := []struct {
		day  string
		want int
	}{{"2026-11-08", 0}, {"2026-11-07", 1}, {"2026-10-30", 9}, {"2026-11-09", 0}}
	for _, c := range cases {
		if got, _ := ageOf("2026-11-08", c.day); got != c.want {
			t.Errorf("ageOf(%s) = %d, want %d", c.day, got, c.want)
		}
	}
}
