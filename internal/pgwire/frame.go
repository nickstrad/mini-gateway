// Package pgwire implements Postgres wire-protocol framing, sans-I/O startup
// and authentication state machines for both client and database legs, pure
// SCRAM-SHA-256 functions, and query-phase stream trackers.
//
// The package performs no I/O, starts no goroutines, reads no wall clock, and
// imports no TLS library; callers drive it with byte slices and lifecycle
// events and execute the returned actions on real sockets or inside the
// deterministic simulator.
package pgwire

import (
	"encoding/binary"
	"errors"
	"fmt"
)

// Framing bounds shared across startup, authentication, and query-phase relay.
const (
	// DefaultMaxMessage caps inspected message bodies when Framer.Max is zero.
	DefaultMaxMessage = 1 << 20
	// MaxStartupPacket caps untyped startup-phase packets (Postgres's 10,000-byte limit).
	MaxStartupPacket = 10_000
	// MaxRelayedMessage caps skipped relayed bodies (Postgres's 1 GiB - 1 limit).
	MaxRelayedMessage = 1<<30 - 1
	untypedHeaderLen  = 4
	typedHeaderLen    = 5
	minStartupLength  = 8
	lengthFieldLen    = 4
)

// Framing error sentinels returned by Framer.Take on impossible or oversized headers.
var (
	// ErrBadLength reports a wire length smaller than its own header minimum.
	ErrBadLength = errors.New("pgwire: impossible message length")
	// ErrTooLarge reports a declared body length exceeding the framer's limit.
	ErrTooLarge = errors.New("pgwire: message larger than the limit")
)

// Frame is one complete wire message produced by Framer.
// Body is nil when Skipped is true and otherwise aliases the framer's internal
// buffer until the next Take call.
type Frame struct {
	Type    byte
	Len     int
	Body    []byte
	Skipped bool
}

// Framer incrementally reassembles Postgres wire messages from arbitrary byte
// splits without reading past the end of the current message.
//
// Set Untyped for startup-phase packets (4-byte length, no type byte); leave
// false for typed messages (1-byte type + 4-byte length). Once an error is
// returned, the framer is latched in that error state.
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

// SkipBodies marks message types whose bodies are validated and counted without
// being copied into the framer's buffer (for example DataRow during relay).
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

// Err returns the latched framing error, if any.
func (f *Framer) Err() error { return f.err }

// Pending reports how many bytes of an incomplete message are currently held.
func (f *Framer) Pending() int {
	if f.done {
		return 0
	}
	return f.hdrN + len(f.body)
}

// BufferCap reports the current capacity of the body scratch buffer.
func (f *Framer) BufferCap() int { return cap(f.body) }

// Frame returns the most recently completed Frame.
func (f *Framer) Frame() Frame { return f.frame }

func (f *Framer) clear() {
	f.hdrN, f.need, f.done = 0, 0, false
	f.body = f.body[:0]
	// Release oversized scratch buffers after one large message so steady-state
	// memory does not stay pinned to a peak frame size.
	if cap(f.body) > frameGrowAhead {
		f.body = nil
	}
	f.frame = Frame{}
}

// Take consumes bytes from b toward at most one message, returning bytes
// consumed, whether a complete Frame is ready via Frame(), and any framing
// error. Oversized or impossible lengths fail as soon as the header completes,
// before buffering any body bytes.
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
	// Only pre-size modest bodies; larger inspected bodies grow lazily as bytes
	// arrive so an advertised-but-abandoned large frame does not force an upfront
	// allocation.
	if !f.frame.Skipped && cap(f.body) < f.need && f.need <= frameGrowAhead {
		f.body = make([]byte, 0, f.need)
	}
	return nil
}

const frameGrowAhead = 64 << 10
