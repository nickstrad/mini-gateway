package fakepg

import (
	"errors"

	"github.com/nickstrad/mini-gateway/internal/obs"
	"github.com/nickstrad/mini-gateway/internal/sim"
)

type RefusedSnapshot struct {
	TooMany         int64
	UnknownUser     int64
	UnknownDatabase int64
	NoUser          int64
	BadStartup      int64
	StartingUp      int64
	Protocol        int64
}

type FaultSnapshot struct {
	ByKind   [NumFaults]int64
	Cleared  int64
	NoTarget int64
}

type Snapshot struct {
	ConnsAccepted       int64
	ConnsClosed         int64
	ConnsOpen           obs.Gauge
	Sessions            obs.Gauge
	StartupsCompleted   int64
	Refused             RefusedSnapshot
	StatementsStarted   int64
	StatementsFinished  int64
	StatementsAborted   int64
	StatementErrors     int64
	StatementsByKind    [NumKinds]int64
	InService           obs.Gauge
	Waiting             obs.Gauge
	WritesApplied       int64
	TxBegun             int64
	TxCommitted         int64
	TxRolledBack        int64
	TxLost              int64
	TxOpen              int64
	CancelsReceived     int64
	CancelsApplied      int64
	CancelsIdle         int64
	CancelsNoMatch      int64
	BytesIn             int64
	BytesOut            int64
	ParamReports        int64
	TableRows           int64
	WriteLogLen         int64
	WriteLogDropped     int64
	StatementLogLen     int64
	StatementLogDropped int64
	Faults              FaultSnapshot
}

func (srv *Server) Snapshot() Snapshot {
	st := &srv.stats
	r := st.refused
	return Snapshot{
		ConnsAccepted:     st.accepted,
		ConnsClosed:       st.closed,
		ConnsOpen:         st.open,
		Sessions:          st.sessions,
		StartupsCompleted: st.startups,
		Refused: RefusedSnapshot{TooMany: r[RefuseTooMany], UnknownUser: r[RefuseUnknownUser],
			UnknownDatabase: r[RefuseUnknownDatabase], NoUser: r[RefuseNoUser], BadStartup: r[RefuseBadStartup],
			StartingUp: r[RefuseStartingUp], Protocol: r[RefuseProtocol]},
		StatementsStarted:   st.started,
		StatementsFinished:  st.finished,
		StatementsAborted:   st.aborted,
		StatementErrors:     st.errors,
		StatementsByKind:    st.byKind,
		InService:           st.inService,
		Waiting:             st.waiting,
		WritesApplied:       st.writes,
		TxBegun:             st.txBegun,
		TxCommitted:         st.txCommitted,
		TxRolledBack:        st.txRolledBack,
		TxLost:              st.txLost,
		TxOpen:              st.txOpen,
		CancelsReceived:     st.cancelsReceived,
		CancelsApplied:      st.cancelsApplied,
		CancelsIdle:         st.cancelsIdle,
		CancelsNoMatch:      st.cancelsNoMatch,
		BytesIn:             st.bytesIn,
		BytesOut:            st.bytesOut,
		ParamReports:        st.paramReports,
		TableRows:           int64(len(srv.table)),
		WriteLogLen:         int64(len(srv.writeLog.items)),
		WriteLogDropped:     srv.writeLog.dropped,
		StatementLogLen:     int64(len(srv.stmtLog.items)),
		StatementLogDropped: srv.stmtLog.dropped,
		Faults:              FaultSnapshot{ByKind: srv.faults.counts, Cleared: srv.faults.cleared, NoTarget: srv.faults.noTarget},
	}
}

func (s Snapshot) Fault(k sim.FaultKind) int64 {
	if k < FaultRefuse || k >= faultEnd {
		return 0
	}
	return s.Faults.ByKind[k-FaultRefuse]
}

func (s Snapshot) Check() error {
	return errors.Join(
		obs.Conserve(obs.T("accepted", s.ConnsAccepted), obs.T("open", s.ConnsOpen.Value), obs.T("closed", s.ConnsClosed)),
		obs.Conserve(obs.T("statements started", s.StatementsStarted), obs.T("finished", s.StatementsFinished),
			obs.T("in service", s.InService.Value), obs.T("waiting", s.Waiting.Value), obs.T("aborted", s.StatementsAborted)),
		obs.Conserve(obs.T("transactions begun", s.TxBegun), obs.T("committed", s.TxCommitted),
			obs.T("rolled back", s.TxRolledBack), obs.T("lost", s.TxLost), obs.T("open", s.TxOpen)),
		obs.Conserve(obs.T("cancels received", s.CancelsReceived), obs.T("applied", s.CancelsApplied),
			obs.T("idle", s.CancelsIdle), obs.T("no match", s.CancelsNoMatch)),
	)
}
