//go:build clscount

package pgwire

const clsCounting = true

type workTally struct{ n int }

type workRef struct{ tally *workTally }

func (w *workTally) bump() { w.n++ }

func (w *workTally) value() int { return w.n }

func (w *workTally) ref() workRef { return workRef{tally: w} }

func (r workRef) bump() { r.tally.n++ }

func (w *workTally) add(n int) { w.n += n }
