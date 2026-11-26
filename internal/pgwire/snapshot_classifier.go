package pgwire

import "github.com/nickstrad/mini-gateway/internal/work"

func (s ClassifierSnapshot) Check() error {
	var sum, reasons int64
	for _, v := range s.ByClass {
		sum += v
	}
	for _, v := range s.ByReason {
		reasons += v
	}
	switch {
	case sum != s.Classified:
		return ClassifErrConservation
	case reasons != s.ByClass[work.ClassReject]:
		return ClassifErrConservation
	case s.Shareable > s.SafeRead, s.SafeRead > s.Classified, s.Opaque > s.Classified, s.Hinted > s.Classified, s.Keys > s.Shareable+s.Hinted:
		return ClassifErrConservation
	}
	return nil
}
