package sim

import (
	"crypto/sha256"
	"encoding/binary"
	"encoding/hex"
	"fmt"
	"hash"
	"io"
	"strings"
)

// Trace ring bounds and wire size of a hashed record.
const (
	// DefaultTraceRing is the ring capacity when Config.TraceRing is not set.
	DefaultTraceRing = 256
	// MaxTraceRing caps the retained tail ring so memory stays bounded.
	MaxTraceRing = 1 << 16
	recordSize   = 48
)

// Kind is a fixed-width, NUL-padded 8-byte tag naming a decision trace record.
type Kind [8]byte

// K converts name into a Kind, silently truncating beyond 8 bytes.
func K(name string) Kind {
	var k Kind
	copy(k[:], name)
	return k
}

// String returns the tag text with trailing NUL padding removed.
func (k Kind) String() string { return strings.TrimRight(string(k[:]), "\x00") }

// Record is one fixed-shape entry in the decision trace: virtual time, an
// 8-byte kind tag, and four signed 64-bit integer arguments. It omits any loop
// sequence number so a fault on one actor does not shift another actor's
// records.
type Record struct {
	At   Time
	Kind Kind
	A    int64
	B    int64
	C    int64
	D    int64
}

// String renders the record as a single human-readable trace line.
func (r Record) String() string {
	return fmt.Sprintf("t=%d %s %d %d %d %d", int64(r.At), r.Kind.String(), r.A, r.B, r.C, r.D)
}

func (r Record) encode(buf *[recordSize]byte) {
	binary.BigEndian.PutUint64(buf[0:], uint64(r.At))
	copy(buf[8:16], r.Kind[:])
	binary.BigEndian.PutUint64(buf[16:], uint64(r.A))
	binary.BigEndian.PutUint64(buf[24:], uint64(r.B))
	binary.BigEndian.PutUint64(buf[32:], uint64(r.C))
	binary.BigEndian.PutUint64(buf[40:], uint64(r.D))
}

type trace struct {
	on    bool
	h     hash.Hash
	ring  []Record
	next  int
	count int64
	tap   func(Record)
	buf   [recordSize]byte
}

func (t *trace) init(on bool, ringSize int) {
	t.on = on
	t.h = sha256.New()
	if ringSize <= 0 {
		ringSize = DefaultTraceRing
	}
	if ringSize > MaxTraceRing {
		ringSize = MaxTraceRing
	}
	if on {
		t.ring = make([]Record, 0, ringSize)
	}
}

func (t *trace) add(r Record) {
	r.encode(&t.buf)
	_, _ = t.h.Write(t.buf[:])
	t.count++
	if len(t.ring) < cap(t.ring) {
		t.ring = append(t.ring, r)
	} else {
		t.ring[t.next] = r
		t.next = (t.next + 1) % len(t.ring)
	}
	if t.tap != nil {
		t.tap(r)
	}
}

func (t *trace) ringLen() int { return len(t.ring) }

func (t *trace) tail(n int) []Record {
	if n <= 0 {
		return nil
	}
	if n > len(t.ring) {
		n = len(t.ring)
	}
	out := make([]Record, 0, n)
	// Before the ring wraps, next is 0; once wrapped, next points at the
	// oldest retained record, so (next+i)%len walks oldest to newest in both cases.
	start := len(t.ring) - n
	for i := start; i < len(t.ring); i++ {
		out = append(out, t.ring[(t.next+i)%len(t.ring)])
	}
	return out
}

// TraceOn reports whether decision tracing is enabled for this run.
func (s *Sim) TraceOn() bool { return s.trace.on }

// Trace appends a record at Now to the running trace digest, the bounded tail
// ring, and any installed trace tap. When tracing is off, Trace is a no-op.
func (s *Sim) Trace(kind Kind, a, b, c, d int64) {
	if !s.trace.on {
		return
	}
	s.trace.add(Record{At: s.now, Kind: kind, A: a, B: b, C: c, D: d})
}

// SetTraceTap installs a callback invoked for every traced record while
// tracing is enabled. Pass nil to clear it.
func (s *Sim) SetTraceTap(fn func(Record)) { s.trace.tap = fn }

// TraceTail returns up to the last n retained trace records in chronological
// order. Non-positive n returns nil.
func (s *Sim) TraceTail(n int) []Record { return s.trace.tail(n) }

// LogLen returns the total number of trace records emitted during the run,
// including records already overwritten in the tail ring.
func (s *Sim) LogLen() int64 { return s.trace.count }

// LogTail returns up to the last n retained trace records formatted as lines.
func (s *Sim) LogTail(n int) []string {
	recs := s.trace.tail(n)
	out := make([]string, 0, len(recs))
	for _, r := range recs {
		out = append(out, r.String())
	}
	return out
}

// WriteLog writes all currently retained trace records to w, one per line.
func (s *Sim) WriteLog(w io.Writer) error {
	for _, r := range s.trace.tail(len(s.trace.ring)) {
		if _, err := io.WriteString(w, r.String()+"\n"); err != nil {
			return err
		}
	}
	return nil
}

// Digest is a SHA-256 hash summarizing either a decision trace or final state.
type Digest [32]byte

// String returns the lowercase hexadecimal encoding of d.
func (d Digest) String() string { return hex.EncodeToString(d[:]) }

// Digests pairs the independent trace and state digests of a simulation run.
type Digests struct {
	Trace Digest
	State Digest
}

// String formats both digests on one line as "trace=<hex> state=<hex>".
func (d Digests) String() string { return "trace=" + d.Trace.String() + " state=" + d.State.String() }

// TraceDigest returns the SHA-256 digest of all trace records emitted so far
// (or the SHA-256 of the empty stream when tracing is off).
func (s *Sim) TraceDigest() Digest {
	var d Digest
	copy(d[:], s.trace.h.Sum(nil))
	return d
}

// StateSink accepts canonical values written by a RegisterState callback into
// the run's StateDigest.
type StateSink interface {
	Int(vs ...int64)
	String(v string)
}

type stateHash struct {
	h   hash.Hash
	buf [8]byte
}

func (w *stateHash) Int(vs ...int64) {
	for _, v := range vs {
		binary.BigEndian.PutUint64(w.buf[:], uint64(v))
		_, _ = w.h.Write(w.buf[:])
	}
}

func (w *stateHash) String(v string) {
	// Prefix strings with their length so adjacent fields cannot collide.
	w.Int(int64(len(v)))
	_, _ = io.WriteString(w.h, v)
}

// RegisterState registers a named state-digest writer. Writers are called in
// registration order, never map order, and names must be unique.
func (s *Sim) RegisterState(name string, fn func(w StateSink)) error {
	for _, st := range s.states {
		if st.name == name {
			err := fmt.Errorf("%w: state %q", ErrDuplicateName, name)
			s.Fail(err)
			return err
		}
	}
	if err := s.register(len(s.states)); err != nil {
		return err
	}
	s.states = append(s.states, stateWriter{name: name, fn: fn})
	return nil
}

// StateDigest computes the SHA-256 digest over every registered state writer
// in registration order. The kernel intentionally excludes its own step and
// fault counters so a cleared fault leaves no residue in state digests.
func (s *Sim) StateDigest() Digest {
	w := &stateHash{h: sha256.New()}
	for _, st := range s.states {
		w.String(st.name)
		st.fn(w)
	}
	var d Digest
	copy(d[:], w.h.Sum(nil))
	return d
}

// Digests returns both the trace digest and the state digest.
func (s *Sim) Digests() Digests { return Digests{Trace: s.TraceDigest(), State: s.StateDigest()} }
