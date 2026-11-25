package fakepg

import (
	"encoding/binary"
	"errors"
	"fmt"
)

// Server-side framing limits matching Postgres's 10,000-byte startup cap.
const (
	MaxStartupPacket  = 10_000
	DefaultMaxMessage = 1 << 20
	frameCompactAt    = 4096
)

var (
	// ErrFrameLength reports a frontend frame whose length field is below its minimum header size.
	ErrFrameLength = errors.New("fakepg: impossible message length")
	// ErrFrameTooLarge reports a frontend frame exceeding MaxStartupPacket or Framer.Max.
	ErrFrameTooLarge = errors.New("fakepg: message larger than the limit")
)

// Framer incrementally reassembles Postgres frontend messages from arbitrary
// byte splits. When Startup is true, it frames untyped startup/SSL/GSS/cancel
// packets (4-byte length only); otherwise it frames typed messages (1-byte type + 4-byte length).
type Framer struct {
	Max     int
	Startup bool
	buf     []byte
	head    int
}

func (f *Framer) limit() int {
	if f.Startup {
		return MaxStartupPacket
	}
	if f.Max <= 0 {
		return DefaultMaxMessage
	}
	return f.Max
}

// Feed appends received bytes to the framer buffer, compacting consumed bytes once
// head passes frameCompactAt or reaches the end of the buffer.
func (f *Framer) Feed(b []byte) {
	if f.head > 0 && (f.head >= frameCompactAt || f.head == len(f.buf)) {
		n := copy(f.buf, f.buf[f.head:])
		f.buf = f.buf[:n]
		f.head = 0
	}
	f.buf = append(f.buf, b...)
}

// Buffered returns the number of unread bytes currently held by the framer.
func (f *Framer) Buffered() int { return len(f.buf) - f.head }

// Next extracts the next complete frontend frame. In Startup mode typ is 0 and
// body begins immediately after the 4-byte length word.
func (f *Framer) Next() (byte, []byte, bool, error) {
	avail := f.buf[f.head:]
	hdr := 5
	if f.Startup {
		hdr = 4
	}
	if len(avail) < hdr {
		return 0, nil, false, nil
	}
	n := int64(binary.BigEndian.Uint32(avail[hdr-4 : hdr]))
	if n < 4 || (f.Startup && n < 8) {
		return 0, nil, false, fmt.Errorf("%w: %d", ErrFrameLength, n)
	}
	if n-4 > int64(f.limit()) {
		return 0, nil, false, fmt.Errorf("%w: %d bytes", ErrFrameTooLarge, n-4)
	}
	total := hdr - 4 + int(n)
	if len(avail) < total {
		return 0, nil, false, nil
	}
	var typ byte
	if !f.Startup {
		typ = avail[0]
	}
	body := avail[hdr:total]
	f.head += total
	return typ, body, true, nil
}

func (f *Framer) TakeRaw(n int) ([]byte, bool) {
	if f.Buffered() < n {
		return nil, false
	}
	out := append([]byte(nil), f.buf[f.head:f.head+n]...)
	f.head += n
	return out, true
}
