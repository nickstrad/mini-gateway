package mgctl

import (
	"errors"
	"fmt"
	"io"
	"strings"
)

const MaxPassword = 1 << 10

var ErrPasswordTooLong = fmt.Errorf("the password line is longer than %d bytes", MaxPassword)

func ReadPasswordLine(r io.Reader) (string, error) {
	b := make([]byte, 0, 64)
	one := make([]byte, 1)
	for {
		n, err := r.Read(one)
		if n == 1 {
			if one[0] == '\n' {
				break
			}
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
