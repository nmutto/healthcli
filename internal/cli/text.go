package cli

import "strings"

func runeLen(s string) int { return len([]rune(s)) }

// stripANSI removes colour codes, to measure the visible width of a line.
func stripANSI(s string) string {
	var b strings.Builder
	for i := 0; i < len(s); i++ {
		if s[i] == 0x1b {
			for i < len(s) && s[i] != 'm' {
				i++
			}
			continue
		}
		b.WriteByte(s[i])
	}
	return b.String()
}

// fit shortens s to at most width characters, ending with an ellipsis.
func fit(s string, width int, g glyphSet) string {
	r := []rune(s)
	if len(r) <= width {
		return s
	}
	e := []rune(g.ellipsis)
	if width <= len(e) {
		return string(r[:max(width, 0)])
	}
	return string(r[:width-len(e)]) + g.ellipsis
}

// plural is "1 sample" or "2 samples" (with thousands separators).
func plural(n int64, noun string) string {
	if n == 1 {
		return "1 " + noun
	}
	return formatInt(float64(n)) + " " + noun + "s"
}

// titleCase upper-cases the first letter: "running" -> "Running".
func titleCase(s string) string {
	if s == "" {
		return s
	}
	return strings.ToUpper(s[:1]) + s[1:]
}

func padLeft(s string, n int) string {
	if w := len([]rune(s)); w < n {
		return strings.Repeat(" ", n-w) + s
	}
	return s
}

// padRight pads s with spaces to n characters (runes, not bytes, so "·" and
// "▲" count as one). Padding is done on plain text BEFORE coloring, because
// invisible ANSI codes would otherwise count toward the width.
func padRight(s string, n int) string {
	if w := len([]rune(s)); w < n {
		return s + strings.Repeat(" ", n-w)
	}
	return s
}

// wrapParts joins parts with sep, starting a new line before a part that
// would run past width columns (colour codes do not count).
func wrapParts(parts []string, sep string, width int) []string {
	var lines []string
	line := ""
	for _, p := range parts {
		if line != "" && runeLen(stripANSI(line))+len(sep)+runeLen(stripANSI(p)) > width {
			lines = append(lines, line)
			line = ""
		}
		if line != "" {
			line += sep
		}
		line += p
	}
	return append(lines, line)
}

// wordWrap breaks text at spaces into lines of at most width characters.
func wordWrap(text string, width int) []string {
	return wrapParts(strings.Fields(text), " ", width)
}
