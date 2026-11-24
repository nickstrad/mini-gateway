package shell

import "example.test/bl/internal/config"

const mailboxCap = 16

const zero = 0

type limits struct {
	cap int
}

type shellConfig struct {
	Mailbox int
}

type Conn struct {
	cfg    shellConfig
	shared *config.Shell
	lim    limits
	n      int
}

func (c *Conn) channels(n int) {
	_ = make(chan int)
	_ = make(chan int, 0)
	_ = make(chan int, zero)
	_ = make(chan int, 8)
	_ = make(chan int, n)
	_ = make(chan int, c.n)
	_ = make(chan int, c.lim.cap)
	_ = make(chan int, 2*mailboxCap)
	_ = make(chan int, mailboxCap)
	_ = make(chan int, (mailboxCap))
	_ = make(chan int, config.DefaultMailbox)
	_ = make(chan int, c.cfg.Mailbox)
	_ = make(chan int, c.shared.Mailbox)
	_ = make([]int, 8)
	_ = make(map[int]int)
}

func allowed() chan struct{} {
	return make(chan struct{})
}

var global = make(chan int)

type named chan int

func namedType() named {
	return make(named)
}

type pipe = chan int

type mailbox chan int

func aliased() (pipe, mailbox) {
	return make(pipe), make(mailbox, 3)
}

func wrapped(n int) chan int {
	return make(chan int, n)
}
