package cli

import (
	"math"
	"sort"
	"strings"
)

// This file holds the small drawing helpers behind sparklines, the baseline
// band and the sleep bar. They are pure functions (text in, text out) so
// tests can check them without a terminal.

// glyphSet is every drawing character the CLI uses. There are two sets: the
// Unicode one for UTF-8 terminals and a plain-ASCII one for pipes, old
// terminals and --ascii. Each set has the same number of bar levels so the
// layout never changes between them.
type glyphSet struct {
	bars     []rune // eight heights, shortest to tallest
	gap      rune   // a day without data
	block    rune   // one full cell, for colour-only heatmaps
	up       string // delta arrows
	down     string
	flat     string
	track    rune // band: outside the usual range
	usual    rune // band: inside the usual range
	marker   rune // band: where the value is
	stages   map[string]rune
	rule     rune
	bandRule rune   // the heavy line of a category band
	arrow    string // "2024-12-01 → 2024-12-15"
	dot      string // separator between facts
	ellipsis string
	box      [6]rune // top-left, top-right, bottom-left, bottom-right, horizontal, vertical
}

var unicodeGlyphs = glyphSet{
	bars: []rune("▁▂▃▄▅▆▇█"), gap: '·', block: '█',
	up: "▲", down: "▼", flat: "–",
	track: '·', usual: '░', marker: '●',
	stages: map[string]rune{"core": '▒', "rem": '▓', "deep": '█', "unspecified": '▒', "awake": '░'},
	rule:   '─', bandRule: '━', arrow: "→", dot: "·", ellipsis: "…",
	box: [6]rune{'┌', '┐', '└', '┘', '─', '│'},
}

var asciiGlyphs = glyphSet{
	bars: []rune(".:-=+*#@"), gap: '_', block: '#',
	up: "^", down: "v", flat: "-",
	track: '.', usual: '=', marker: '*',
	stages: map[string]rune{"core": ':', "rem": '+', "deep": '#', "unspecified": '~', "awake": '.'},
	rule:   '-', bandRule: '=', arrow: "->", dot: "|", ellipsis: "...",
	box: [6]rune{'+', '+', '+', '+', '-', '|'},
}

// glyphsFor picks the set for a terminal.
func glyphsFor(unicode bool) glyphSet {
	if unicode {
		return unicodeGlyphs
	}
	return asciiGlyphs
}

// sparkline draws one character per entry. NaN means "no data that day".
// Bars are scaled between the minimum and maximum of the data shown, so it
// shows the shape of the trend, not absolute size (min/max are printed too).
func sparkline(values []float64) string { return unicodeGlyphs.sparkline(values) }

func (g glyphSet) sparkline(values []float64) string {
	lo, hi := math.Inf(1), math.Inf(-1)
	for _, v := range values {
		if math.IsNaN(v) {
			continue
		}
		lo, hi = math.Min(lo, v), math.Max(hi, v)
	}
	out := make([]rune, 0, len(values))
	for _, v := range values {
		switch {
		case math.IsNaN(v):
			out = append(out, g.gap)
		case hi == lo:
			// Every value is the same: a flat middle line, not a wall of
			// full-height bars that would suggest a peak.
			out = append(out, g.bars[len(g.bars)/2-1])
		default:
			// With an infinite value in the data the ratio is NaN or Inf, and
			// converting that to int is machine-dependent: clamp first.
			pos := math.Round((v - lo) / (hi - lo) * float64(len(g.bars)-1))
			if math.IsNaN(pos) {
				pos = 0
			}
			out = append(out, g.bars[int(math.Max(0, math.Min(float64(len(g.bars)-1), pos)))])
		}
	}
	return string(out)
}

// deltaArrow is the arrow for a percentage change. A change that rounds to
// 0% is "flat": showing an up arrow next to "+0%" would be misleading.
func (g glyphSet) deltaArrow(pct float64) string {
	switch r := math.Round(pct); {
	case r > 0:
		return g.up
	case r < 0:
		return g.down
	}
	return g.flat
}

// bandBar draws a track of `width` cells that answers "is this value normal?".
//
// The track covers mean±3 standard deviations. The cells within mean±1
// standard deviation (the "typical" range used by the above/below labels) are
// shaded, and the value is a marker. A value beyond the track is pinned to
// the nearest end, so an extreme value still shows up at the edge.
//
//	····░░░░░●····    (usual range in the middle, value a little above it)
func (g glyphSet) bandBar(value, mean, stddev float64, width int) string {
	if width < 3 {
		return strings.Repeat(string(g.track), max(width, 0))
	}
	if stddev <= 0 {
		// Every baseline day was identical, so there is no spread. Pretend
		// the spread is 5% of the mean so the band still has some width.
		stddev = math.Max(math.Abs(mean)*0.05, 1e-9)
	}
	lo, hi := mean-3*stddev, mean+3*stddev

	// cell maps a number to a cell index 0..width-1. Rounding to the nearest
	// cell (not truncating) keeps the mean exactly in the middle cell.
	cell := func(x float64) int {
		i := int(math.Round((x - lo) / (hi - lo) * float64(width-1)))
		return min(max(i, 0), width-1)
	}

	cells := make([]rune, width)
	for i := range cells {
		cells[i] = g.track
	}
	for i := cell(mean - stddev); i <= cell(mean+stddev); i++ {
		cells[i] = g.usual
	}
	cells[cell(value)] = g.marker
	return string(cells)
}

// segment is one slice of a stacked bar.
type segment struct {
	stage string // key into glyphSet.stages
	size  int64  // any unit; only the proportions matter
}

// stackedBar draws the segments side by side across exactly `width` cells,
// each stage in its own glyph. Cell counts come from largestRemainder, so the
// bar is always exactly `width` wide. A segment too small to earn a whole
// cell disappears from the bar (the legend under it still lists it).
func (g glyphSet) stackedBar(segs []segment, width int) string {
	return g.stackedBarPainted(segs, width, nil)
}

// stackedBarPainted is stackedBar with a colour per segment: paint gets the
// stage name and that segment's run of glyphs. A nil paint leaves it plain.
func (g glyphSet) stackedBarPainted(segs []segment, width int, paint func(stage, run string) string) string {
	sizes := make([]int64, len(segs))
	for i, s := range segs {
		sizes[i] = s.size
	}
	var b strings.Builder
	for i, n := range largestRemainder(sizes, width) {
		run := strings.Repeat(string(g.stages[segs[i].stage]), n)
		if paint != nil && n > 0 {
			run = paint(segs[i].stage, run)
		}
		b.WriteString(run)
	}
	return b.String()
}

// largestRemainder splits `total` whole units across parts in proportion to
// their sizes, so the pieces always add up to exactly total.
//
// Plain rounding can't promise that: three equal parts of 100 would each round
// to 33 and lose one. Instead: give every part the whole-number part of its
// share (rounded down), then hand the leftover units, one each, to the parts
// with the biggest fractional leftovers. Ties go to the earlier part, so the
// result never depends on map or sort order.
func largestRemainder(parts []int64, total int) []int {
	out := make([]int, len(parts))
	var sum int64
	for _, p := range parts {
		sum += max(p, 0)
	}
	if sum == 0 || total <= 0 {
		return out
	}

	type leftover struct {
		idx  int
		frac float64
	}
	var rest []leftover
	given := 0
	for i, p := range parts {
		share := float64(max(p, 0)) / float64(sum) * float64(total)
		out[i] = int(math.Floor(share))
		given += out[i]
		rest = append(rest, leftover{i, share - math.Floor(share)})
	}
	sort.SliceStable(rest, func(a, b int) bool { return rest[a].frac > rest[b].frac })
	for _, l := range rest {
		if given >= total {
			break
		}
		// Never give a unit to a part of size 0.
		if parts[l.idx] > 0 {
			out[l.idx]++
			given++
		}
	}
	return out
}
