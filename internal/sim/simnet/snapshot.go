package simnet

type DirSnapshot struct {
	Written       int64
	Delivered     int64
	Discarded     int64
	InFlight      int64
	WritesRefused int64
	Chunks        int64
}

type FaultSnapshot struct {
	Reset     int64
	Stall     int64
	HalfOpen  int64
	HalfClose int64
	Chunk     int64
	Refuse    int64
	DialStall int64
	Silence   int64
	Clear     int64
	NoTarget  int64
}

type ClosedSnapshot struct {
	Graceful int64
	Reset    int64
	HalfOpen int64
	Silent   int64
	Aborted  int64
}

type Snapshot struct {
	Listeners       int64
	DialsAttempted  int64
	DialsRefused    int64
	DialsStalled    int64
	ConnsOpened     int64
	ConnsOpen       int64
	ConnsConnecting int64
	Closed          ClosedSnapshot
	Up              DirSnapshot
	Down            DirSnapshot
	Faults          FaultSnapshot
}

func (n *Net) Snapshot() Snapshot {
	st := &n.stats
	live := int64(0)
	for _, l := range n.listeners {
		if !l.closed {
			live++
		}
	}
	return Snapshot{
		Listeners:       live,
		DialsAttempted:  st.dialsAttempted,
		DialsRefused:    st.dialsRefused,
		DialsStalled:    st.dialsStalled,
		ConnsOpened:     st.opened,
		ConnsOpen:       st.open,
		ConnsConnecting: st.connecting,
		Closed: ClosedSnapshot{
			Graceful: st.closed[ReasonGraceful],
			Reset:    st.closed[ReasonReset],
			HalfOpen: st.closed[ReasonHalfOpen],
			Silent:   st.closed[ReasonSilent],
			Aborted:  st.closed[ReasonAborted],
		},
		Up:     st.dir[up],
		Down:   st.dir[down],
		Faults: st.faults,
	}
}
