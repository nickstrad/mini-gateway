//go:build !clscount

package clscount

const On = false

type Tally struct{}

type Ref struct{}

func (*Tally) Bump() {}

func (*Tally) Add(int) {}

func (*Tally) Value() int { return 0 }

func (*Tally) Ref() Ref { return Ref{} }

func (Ref) Bump() {}
