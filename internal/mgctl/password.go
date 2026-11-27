package mgctl

import (
	"errors"
	"fmt"
	"io"
	"strings"
)

// MaxPassword is the maximum byte length accepted for a password read from stdin (excluding trailing CR/LF).
const MaxPassword = 1 << 10

// ErrPasswordTooLong is returned when a stdin password line exceeds MaxPassword bytes.
var ErrPasswordTooLong = fmt.Errorf("the password line is longer than %d bytes", MaxPassword)

// ReadPasswordLine reads the first line from r one byte at a time (so nothing
// past the newline is buffered), strips trailing '\r', and refuses lines longer
// than MaxPassword bytes instead of silently truncating them.
func ReadPasswordLine(r io.Reader) (string, error) {
	b := make([]byte, 0, 64)
	one := make([]byte, 1)
	for {
		n, err := r.Read(one)
		if n == 1 {
			if one[0] == '\n' {
				break
			}
			// Allow one extra byte before checking so a 1024-byte password followed by "\r\n" is accepted.
			if len(b) > MaxPassword {
				return "", ErrPasswordTooLong
			}
			b = append(b, one[0])
			continue
		}
		if errors.Is(err, io.EOF) {
			break
		}
		if err != nil {
			return "", err
		}
	}
	line := strings.TrimRight(string(b), "\r")
	if len(line) > MaxPassword {
		return "", ErrPasswordTooLong
	}
	return line, nil
}
