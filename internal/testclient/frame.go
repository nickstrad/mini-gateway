package testclient

import (
	"encoding/binary"
	"errors"
	"fmt"
)

const (
	DefaultMaxMessage = 32 << 20
	headerLen         = 5
	compactAt         = 4096
)

var (
	ErrBadLength = errors.New("testclient: impossible message length")
	ErrTooLarge  = errors.New("testclient: message larger than the limit")
)

type Framer struct {
	Max  int
	buf  []byte
	head int
}

func (f *Framer) max() int {
	if f.Max <= 0 {
		return DefaultMaxMessage
	}
	return f.Max
}

func (f *Framer) Feed(b []byte) {
	if f.head > 0 && (f.head >= compactAt || f.head == len(f.buf)) {
		n := copy(f.buf, f.buf[f.head:])
		f.buf = f.buf[:n]
		f.head = 0
	}
	f.buf = append(f.buf, b...)
}

func (f *Framer) Buffered() int { return len(f.buf) - f.head }

func (f *Framer) TakeByte() (byte, bool) {
	if f.Buffered() == 0 {
		return 0, false
	}
	b := f.buf[f.head]
	f.head++
	return b, true
}

func (f *Framer) Next() (byte, []byte, bool, error) {
	avail := f.buf[f.head:]
	if len(avail) < headerLen {
		return 0, nil, false, nil
	}
	n := int64(binary.BigEndian.Uint32(avail[1:headerLen]))
	if n < 4 || n > int64(0x7fffffff) {
		return 0, nil, false, fmt.Errorf("%w: %d", ErrBadLength, n)
	}
	if n-4 > int64(f.max()) {
		return 0, nil, false, fmt.Errorf("%w: %d bytes", ErrTooLarge, n-4)
	}
	total := 1 + int(n)
	if len(avail) < total {
		return 0, nil, false, nil
	}
	typ := avail[0]
	body := avail[headerLen:total]
	f.head += total
	return typ, body, true, nil
}
