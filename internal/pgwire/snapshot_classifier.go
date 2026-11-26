package pgwire

import "github.com/nickstrad/mini-gateway/internal/work"

const (
	ClassifComponent = "classifier"
	ReplyComponent   = "wire"
)

type ClassifierMetric struct {
	Component string
	Metric    string
	Outcome   string
	Reason    string
	Value     int64
}

func ClassifierMetrics(s ClassifierSnapshot) []ClassifierMetric {
	out := make([]ClassifierMetric, 0, 8+int(work.NumClasses)+int(work.NumRejectReasons)+3)
	add := func(metric, outcome, reason string, v int64) {
		out = append(out, ClassifierMetric{Component: ClassifComponent, Metric: metric, Outcome: outcome, Reason: reason, Value: v})
	}
	add("statements", "", "", s.Classified)
	for c := work.ClassReject; c < work.NumClasses; c++ {
		add("statements_by_class", c.String(), "", s.ByClass[c])
	}
	for r := work.RejectSessionSet; r < work.NumRejectReasons; r++ {
		add("rejects", "", r.String(), s.ByReason[r])
	}
	add("unclassifiable", "", "", s.Unclassifiable)
	add("opaque", "", "", s.Opaque)
	add("read_only", "", "", s.ReadOnly)
	add("coalescable", "", "", s.Coalescable)
	add("hinted", "", "", s.Hinted)
	add("sql_statements", "", "", s.Statements)
	for t := work.No; t <= work.Unknown; t++ {
		add("may_leave_open", t.String(), "", s.MayLeaveOpen[t])
	}
	return out
}

func ReplyMetrics(s ReplySnapshot) []ClassifierMetric {
	out := make([]ClassifierMetric, 0, int(ErrRowCount)+2)
	for id := ErrRowID(0); id < ErrRowCount; id++ {
		out = append(out, ClassifierMetric{Component: ReplyComponent, Metric: "proxy_replies", Reason: ErrorRows[id].ID, Value: s.ByRow[id]})
	}
	out = append(out, ClassifierMetric{Component: ReplyComponent, Metric: "proxy_replies_total", Value: s.Total})
	out = append(out, ClassifierMetric{Component: ReplyComponent, Metric: "proxy_reply_failures", Value: s.Failed})
	return out
}

func (s ClassifierSnapshot) Check() error {
	var sum, reasons, open int64
	for _, v := range s.ByClass {
		sum += v
	}
	for _, v := range s.ByReason {
		reasons += v
	}
	for _, v := range s.MayLeaveOpen {
		open += v
	}
	switch {
	case sum != s.Classified:
		return ClassifErrConservation
	case reasons != s.ByClass[work.ClassReject]:
		return ClassifErrConservation
	case open != s.Classified:
		return ClassifErrConservation
	case s.Coalescable > s.ReadOnly:
		return ClassifErrConservation
	}
	return nil
}
