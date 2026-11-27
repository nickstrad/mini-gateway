//go:build clscount

package clscount

const On = true

type Tally struct{ n int }

type Ref struct{ tally *Tally }

func (t *Tally) Bump() { t.n++ }

func (t *Tally) Add(n int) { t.n += n }

func (t *Tally) Value() int { return t.n }

func (t *Tally) Ref() Ref { return Ref{tally: t} }

func (r Ref) Bump() { r.tally.n++ }
