package testclient

import (
	"encoding/binary"
	"errors"
	"fmt"
)

// Framing limits and compaction threshold for backend-to-client messages.
const (
	// DefaultMaxMessage is the default upper bound on a single backend message body.
	DefaultMaxMessage = 32 << 20
	headerLen         = 5
	compactAt         = 4096
)

var (
	// ErrBadLength reports a backend frame whose 4-byte length field is below 4 or exceeds 0x7fffffff.
	ErrBadLength = errors.New("testclient: impossible message length")
	// ErrTooLarge reports a backend frame whose body exceeds Framer.Max.
	ErrTooLarge = errors.New("testclient: message larger than the limit")
)

// Framer incrementally reassembles Postgres backend messages from arbitrarily
// chunked byte slices (down to one byte at a time) without blocking I/O.
type Framer struct {
	// Max bounds the body length of one backend message; zero uses DefaultMaxMessage.
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

// Feed appends newly received bytes to the framer buffer, compacting consumed
// prefix bytes once head reaches compactAt or the buffer empties.
func (f *Framer) Feed(b []byte) {
	if f.head > 0 && (f.head >= compactAt || f.head == len(f.buf)) {
		n := copy(f.buf, f.buf[f.head:])
		f.buf = f.buf[:n]
		f.head = 0
	}
	f.buf = append(f.buf, b...)
}

// Buffered reports how many unread bytes are currently held in the framer.
func (f *Framer) Buffered() int { return len(f.buf) - f.head }

// TakeByte consumes a single raw byte, used for the untyped one-byte ('S'/'N')
// reply to an SSLRequest or GSSENCRequest before typed framing begins.
func (f *Framer) TakeByte() (byte, bool) {
	if f.Buffered() == 0 {
		return 0, false
	}
	b := f.buf[f.head]
	f.head++
	return b, true
}

// Next returns the next complete typed backend message (type byte and body slice
// without the 4-byte length field). The returned body aliases internal storage
// until the next Feed call compacts the buffer.
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
