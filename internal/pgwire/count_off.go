//go:build !clscount

package pgwire

const clsCounting = false

type workTally struct{}

type workRef struct{}

func (*workTally) bump() {}

func (*workTally) value() int { return 0 }

func (*workTally) ref() workRef { return workRef{} }

func (workRef) bump() {}

func (*workTally) add(int) {}
