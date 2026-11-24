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

const (
	DefaultTraceRing = 256
	MaxTraceRing     = 1 << 16
	recordSize       = 48
)

type Kind [8]byte

func K(name string) Kind {
	var k Kind
	copy(k[:], name)
	return k
}

func (k Kind) String() string { return strings.TrimRight(string(k[:]), "\x00") }

type Record struct {
	At   Time
	Kind Kind
	A    int64
	B    int64
	C    int64
	D    int64
}

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
	if n > len(t.ring) {
		n = len(t.ring)
	}
	out := make([]Record, 0, n)
	start := len(t.ring) - n
	for i := start; i < len(t.ring); i++ {
		out = append(out, t.ring[(t.next+i)%len(t.ring)])
	}
	return out
}

func (s *Sim) TraceOn() bool { return s.trace.on }

func (s *Sim) Trace(kind Kind, a, b, c, d int64) {
	if !s.trace.on {
		return
	}
	s.trace.add(Record{At: s.now, Kind: kind, A: a, B: b, C: c, D: d})
}

func (s *Sim) SetTraceTap(fn func(Record)) { s.trace.tap = fn }

func (s *Sim) TraceTail(n int) []Record { return s.trace.tail(n) }

func (s *Sim) LogLen() int64 { return s.trace.count }

func (s *Sim) LogTail(n int) []string {
	recs := s.trace.tail(n)
	out := make([]string, 0, len(recs))
	for _, r := range recs {
		out = append(out, r.String())
	}
	return out
}

func (s *Sim) WriteLog(w io.Writer) error {
	for _, r := range s.trace.tail(len(s.trace.ring)) {
		if _, err := io.WriteString(w, r.String()+"\n"); err != nil {
			return err
		}
	}
	return nil
}

type Digest [32]byte

func (d Digest) String() string { return hex.EncodeToString(d[:]) }

type Digests struct {
	Trace Digest
	State Digest
}

func (d Digests) String() string { return "trace=" + d.Trace.String() + " state=" + d.State.String() }

func (s *Sim) TraceDigest() Digest {
	var d Digest
	copy(d[:], s.trace.h.Sum(nil))
	return d
}

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
	w.Int(int64(len(v)))
	_, _ = io.WriteString(w.h, v)
}

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

func (s *Sim) Digests() Digests { return Digests{Trace: s.TraceDigest(), State: s.StateDigest()} }
