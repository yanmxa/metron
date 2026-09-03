package config

import (
	"bytes"
	"io"
)

// newTrimmer strips // line comments so a config can explain itself.
//
// JSON has no comments, and a threshold with no recorded reason gets raised by
// the next person who hits it. Anything inside a string is left alone.
func newTrimmer(buf []byte) io.Reader {
	out := make([]byte, 0, len(buf))
	inString, escaped := false, false

	for i := 0; i < len(buf); i++ {
		c := buf[i]
		switch {
		case escaped:
			escaped = false
		case inString && c == '\\':
			escaped = true
		case c == '"':
			inString = !inString
		case !inString && c == '/' && i+1 < len(buf) && buf[i+1] == '/':
			for i < len(buf) && buf[i] != '\n' {
				i++
			}
			if i < len(buf) {
				out = append(out, '\n')
			}
			continue
		}
		out = append(out, c)
	}
	return bytes.NewReader(out)
}
