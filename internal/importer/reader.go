package importer

import (
	"errors"
	"io"
	"unicode/utf8"
)

// maxTokenBytes is the longest stretch of XML we accept without a '>'. The
// XML decoder keeps a whole tag (all its attributes) in memory, so a hostile
// file with one gigantic attribute could use all the memory. A real record
// tag is under 2 KB; 16 MiB is far beyond anything honest.
const maxTokenBytes = 16 << 20

var errTokenTooBig = errors.New("an XML tag or text run is longer than 16 MiB")

// replacement is U+FFFD, written in place of every byte the XML decoder would
// refuse.
const replacement = "�"

// cleanReader sits between the file and the XML decoder. Go's decoder stops
// with an error at the first byte that is not valid UTF-8 (or is a control
// character XML forbids). One such byte in millions of records would abort the
// whole import, so we swap it for U+FFFD and count it. It also enforces
// maxTokenBytes.
type cleanReader struct {
	src      io.Reader
	in       []byte // read buffer; the first `carry` bytes are an unfinished character from the last read
	carry    int
	out      []byte // cleaned bytes not yet handed out
	outPos   int
	run      int   // bytes since the last '>'
	replaced int64 // bytes replaced so far
	err      error // reported once `out` is empty
}

func newCleanReader(src io.Reader) *cleanReader {
	return &cleanReader{src: src, in: make([]byte, 64*1024)}
}

func (c *cleanReader) Read(p []byte) (int, error) {
	for c.outPos >= len(c.out) {
		if c.err != nil {
			return 0, c.err
		}
		c.fill()
	}
	n := copy(p, c.out[c.outPos:])
	c.outPos += n
	return n, nil
}

// fill reads one chunk from the source and cleans it into c.out.
func (c *cleanReader) fill() {
	n, err := c.src.Read(c.in[c.carry:])
	atEOF := err != nil // any error ends the stream for us
	chunk := c.in[:c.carry+n]
	c.out, c.outPos = c.out[:0], 0
	c.carry = 0

	for i := 0; i < len(chunk); {
		b := chunk[i]
		if b < utf8.RuneSelf {
			// XML allows tab, newline and carriage return; other control
			// characters are illegal.
			if b < 0x20 && b != '\t' && b != '\n' && b != '\r' {
				c.out = append(c.out, replacement...)
				c.replaced++
			} else {
				c.out = append(c.out, b)
			}
			if b == '>' {
				c.run = 0
			} else {
				c.run++
			}
			i++
			if c.run > maxTokenBytes {
				err = errTokenTooBig
				break
			}
			continue
		}
		// A multi-byte character may be cut in half by the end of the chunk:
		// keep its first bytes for the next round.
		if !atEOF && !utf8.FullRune(chunk[i:]) {
			c.carry = copy(c.in, chunk[i:])
			break
		}
		r, size := utf8.DecodeRune(chunk[i:])
		switch {
		case r == utf8.RuneError && size == 1, r == 0xFFFE, r == 0xFFFF:
			c.out = append(c.out, replacement...)
			c.replaced++
		default:
			c.out = append(c.out, chunk[i:i+size]...)
		}
		c.run += size
		i += size
		if c.run > maxTokenBytes {
			err = errTokenTooBig
			break
		}
	}

	if err != nil {
		c.err = err
	}
}
