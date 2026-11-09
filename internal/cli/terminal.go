package cli

import (
	"io"
	"os"
	"strconv"
	"strings"

	"golang.org/x/term"
)

// termInfo is everything the overview renderer needs to know about the
// output. render functions take it as a plain value, so tests can render at
// any width, with or without colour, without a real terminal.
type termInfo struct {
	width   int  // columns, always within minWidth..maxWidth
	color   bool // use ANSI colours
	unicode bool // use Unicode glyphs (otherwise plain ASCII)
}

const (
	minWidth     = 50
	maxWidth     = 100
	defaultWidth = 80
)

// detectTerm looks at the output and the environment. getenv is a parameter
// (instead of os.Getenv) so tests can pass a fake environment.
//
// Colour: --no-color or NO_COLOR always turn it off; otherwise FORCE_COLOR
// turns it on even for a pipe; otherwise it is on only for a real terminal.
// Unicode: only for a real terminal in a UTF-8 locale, and never with --ascii.
// So a pipe gets plain ASCII, which is safe to paste anywhere.
func detectTerm(w io.Writer, getenv func(string) string, noColorFlag, asciiFlag bool) termInfo {
	f, isFile := w.(*os.File)
	isTTY := isFile && term.IsTerminal(int(f.Fd()))

	ti := termInfo{width: defaultWidth}

	// Width: $COLUMNS first (the user's explicit say), then ask the terminal.
	if n, err := strconv.Atoi(getenv("COLUMNS")); err == nil && n > 0 {
		ti.width = n
	} else if isTTY {
		if cols, _, err := term.GetSize(int(f.Fd())); err == nil && cols > 0 {
			ti.width = cols
		}
	}
	ti.width = min(max(ti.width, minWidth), maxWidth)

	switch {
	case noColorFlag || getenv("NO_COLOR") != "":
		ti.color = false
	case getenv("FORCE_COLOR") != "" && getenv("FORCE_COLOR") != "0":
		ti.color = true
	default:
		ti.color = isTTY && getenv("TERM") != "dumb"
	}

	ti.unicode = isTTY && !asciiFlag && utf8Locale(getenv)
	return ti
}

// utf8Locale reports whether the locale says UTF-8. The first of LC_ALL,
// LC_CTYPE and LANG that is set decides, as in POSIX.
func utf8Locale(getenv func(string) string) bool {
	for _, name := range []string{"LC_ALL", "LC_CTYPE", "LANG"} {
		if v := strings.ToLower(getenv(name)); v != "" {
			return strings.Contains(v, "utf-8") || strings.Contains(v, "utf8")
		}
	}
	return false
}
