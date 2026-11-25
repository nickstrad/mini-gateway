package pgwire

import (
	"encoding/binary"
	"errors"
	"fmt"
)

const (
	DefaultMaxMessage = 1 << 20
	MaxStartupPacket  = 10_000
	MaxRelayedMessage = 1<<30 - 1
	untypedHeaderLen  = 4
	typedHeaderLen    = 5
	minStartupLength  = 8
	lengthFieldLen    = 4
)

var (
	ErrBadLength = errors.New("pgwire: impossible message length")
	ErrTooLarge  = errors.New("pgwire: message larger than the limit")
)

type Frame struct {
	Type    byte
	Len     int
	Body    []byte
	Skipped bool
}

type Framer struct {
	Max     int
	Untyped bool
	skip    [256]bool
	hdr     [typedHeaderLen]byte
	hdrN    int
	need    int
	body    []byte
	frame   Frame
	done    bool
	err     error
}

func (f *Framer) SkipBodies(types ...byte) {
	for _, t := range types {
		f.skip[t] = true
	}
}

func (f *Framer) limit() int {
	if f.Untyped {
		return MaxStartupPacket
	}
	if f.Max <= 0 {
		return DefaultMaxMessage
	}
	return f.Max
}

func (f *Framer) headerLen() int {
	if f.Untyped {
		return untypedHeaderLen
	}
	return typedHeaderLen
}

func (f *Framer) Err() error { return f.err }

func (f *Framer) Pending() int { return f.hdrN + len(f.body) }

func (f *Framer) BufferCap() int { return cap(f.body) }

func (f *Framer) Frame() Frame { return f.frame }

func (f *Framer) clear() {
	f.hdrN, f.need, f.done = 0, 0, false
	f.body = f.body[:0]
	if cap(f.body) > frameGrowAhead {
		f.body = nil
	}
	f.frame = Frame{}
}

func (f *Framer) Take(b []byte) (int, bool, error) {
	if f.err != nil {
		return 0, false, f.err
	}
	if f.done {
		f.clear()
	}
	n := 0
	hl := f.headerLen()
	if f.hdrN < hl {
		c := copy(f.hdr[f.hdrN:hl], b)
		f.hdrN += c
		n += c
		if f.hdrN < hl {
			return n, false, nil
		}
		if err := f.parseHeader(); err != nil {
			f.err = err
			return n, false, err
		}
	}
	if f.need > 0 {
		c := min(f.need, len(b)-n)
		if !f.frame.Skipped {
			f.body = append(f.body, b[n:n+c]...)
		}
		f.need -= c
		n += c
	}
	if f.need > 0 {
		return n, false, nil
	}
	f.done = true
	if !f.frame.Skipped {
		f.frame.Body = f.body
	}
	return n, true, nil
}

func (f *Framer) parseHeader() error {
	var length int64
	if f.Untyped {
		length = int64(binary.BigEndian.Uint32(f.hdr[:untypedHeaderLen]))
		if length < minStartupLength {
			return fmt.Errorf("%w: %d", ErrBadLength, length)
		}
	} else {
		f.frame.Type = f.hdr[0]
		length = int64(binary.BigEndian.Uint32(f.hdr[1:typedHeaderLen]))
		if length < lengthFieldLen {
			return fmt.Errorf("%w: %d", ErrBadLength, length)
		}
		f.frame.Skipped = f.skip[f.frame.Type]
	}
	bodyLen := length - lengthFieldLen
	limit := int64(f.limit())
	if f.frame.Skipped {
		limit = MaxRelayedMessage
	}
	if bodyLen > limit {
		return fmt.Errorf("%w: %d bytes", ErrTooLarge, bodyLen)
	}
	f.frame.Len = int(bodyLen)
	f.need = int(bodyLen)
	if !f.frame.Skipped && cap(f.body) < f.need && f.need <= frameGrowAhead {
		f.body = make([]byte, 0, f.need)
	}
	return nil
}

const frameGrowAhead = 64 << 10
