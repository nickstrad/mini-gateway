package proxy

import "encoding/binary"

const headerLen = 5

type msgCursor struct {
	hdr    [headerLen]byte
	hn     int
	remain int
	bad    bool
}

func (m *msgCursor) mid() bool { return m.hn > 0 }

func (m *msgCursor) typ() byte { return m.hdr[0] }

func (m *msgCursor) atStart() bool { return m.hn == 0 }

func (m *msgCursor) next(b []byte) (int, bool) {
	if m.bad {
		return len(b), false
	}
	n := 0
	if m.hn < headerLen {
		k := min(headerLen-m.hn, len(b))
		copy(m.hdr[m.hn:], b[:k])
		m.hn += k
		n = k
		if m.hn < headerLen {
			return n, false
		}
		length := int64(binary.BigEndian.Uint32(m.hdr[1:]))
		if length < 4 {
			m.bad = true
			return len(b), false
		}
		m.remain = int(length - 4)
		if m.remain == 0 {
			m.hn = 0
			return n, true
		}
	}
	k := min(m.remain, len(b)-n)
	m.remain -= k
	n += k
	if m.remain == 0 {
		m.hn = 0
		return n, true
	}
	return n, false
}
