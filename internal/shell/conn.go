package shell

import (
	"context"
	"net"
	"sync"
	"time"

	"github.com/nickstrad/mini-gateway/internal/proxy"
)

type side uint8

const (
	sideClient side = iota
	sideDB
)

type readMode uint8

const (
	readFirst readMode = iota
	readWait
	readFree
)

type itemKind uint8

const (
	itemWrite itemKind = iota + 1
	itemStartTLS
	itemClose
)

type writeItem struct {
	kind itemKind
	data []byte
}

type conn struct {
	side  side
	id    proxy.ConnID
	start time.Time
	first int

	readBell   chan struct{}
	writeBell  chan struct{}
	done       chan struct{}
	writerDone chan struct{}

	mu           sync.Mutex
	nc           net.Conn
	mode         readMode
	inflight     bool
	queue        []writeItem
	queued       int
	aborted      bool
	engineClosed bool
	closePosted  bool
	unwritten    bool
	cancelDial   context.CancelFunc
}

func newConn(s side, id proxy.ConnID, start time.Time, first int) *conn {
	c := &conn{side: s, id: id, start: start, first: first,
		readBell: make(chan struct{}, bellCap), writeBell: make(chan struct{}, bellCap), done: make(chan struct{}, bellCap),
		writerDone: make(chan struct{})}
	if first == 0 {
		c.mode = readFree
	}
	return c
}

func ring(bell chan struct{}) {
	select {
	case bell <- struct{}{}:
	default:
	}
}

func (c *conn) current() net.Conn {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.nc
}

func (c *conn) attach(nc net.Conn) bool {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.aborted {
		return false
	}
	c.nc = nc
	c.cancelDial = nil
	return true
}

func (c *conn) setCancel(cancel context.CancelFunc) bool {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.aborted {
		return false
	}
	c.cancelDial = cancel
	return true
}

func (c *conn) setMode(m readMode) {
	c.mu.Lock()
	c.mode = m
	c.mu.Unlock()
	ring(c.readBell)
}

func (c *conn) setInflight(on bool) {
	c.mu.Lock()
	c.inflight = on
	c.mu.Unlock()
	if !on {
		ring(c.readBell)
	}
}

func (c *conn) firstReadDone() {
	c.mu.Lock()
	if c.mode == readFirst {
		c.mode = readWait
	}
	c.mu.Unlock()
}

func (c *conn) awaitRead() (net.Conn, int, bool) {
	for {
		c.mu.Lock()
		aborted, mode, nc, inflight := c.aborted, c.mode, c.nc, c.inflight
		c.mu.Unlock()
		switch {
		case aborted:
			return nil, 0, false
		case inflight:
		case mode == readFirst:
			return nc, c.first, true
		case mode == readFree:
			return nc, 0, true
		}
		select {
		case <-c.readBell:
		case <-c.done:
		}
	}
}

func (c *conn) enqueue(it writeItem) bool {
	c.mu.Lock()
	if c.aborted || c.engineClosed {
		c.mu.Unlock()
		return true
	}
	if len(c.queue) >= maxQueueItems || c.queued+len(it.data) > writeQueueCap {
		c.mu.Unlock()
		return false
	}
	c.queue = append(c.queue, it)
	c.queued += len(it.data)
	c.mu.Unlock()
	ring(c.writeBell)
	return true
}

func (c *conn) closeByEngine() bool {
	c.mu.Lock()
	if c.aborted || c.engineClosed {
		c.mu.Unlock()
		return true
	}
	c.engineClosed = true
	if len(c.queue) >= maxQueueItems+closeSlack {
		c.mu.Unlock()
		return false
	}
	c.queue = append(c.queue, writeItem{kind: itemClose})
	c.mu.Unlock()
	ring(c.writeBell)
	return true
}

func (c *conn) next() (writeItem, bool) {
	for {
		c.mu.Lock()
		if c.aborted {
			c.mu.Unlock()
			return writeItem{}, false
		}
		if len(c.queue) > 0 {
			it := c.queue[0]
			c.queue[0] = writeItem{}
			c.queue = c.queue[1:]
			c.queued -= len(it.data)
			c.mu.Unlock()
			return it, true
		}
		c.mu.Unlock()
		select {
		case <-c.writeBell:
		case <-c.done:
		}
	}
}

func (c *conn) abort() {
	c.mu.Lock()
	if c.aborted {
		c.mu.Unlock()
		return
	}
	c.aborted = true
	for _, it := range c.queue {
		if it.kind == itemWrite && len(it.data) > 0 {
			c.unwritten = true
		}
	}
	nc, cancel := c.nc, c.cancelDial
	c.queue, c.queued, c.cancelDial = nil, 0, nil
	close(c.done)
	c.mu.Unlock()
	if nc != nil {
		_ = nc.Close()
	}
	if cancel != nil {
		cancel()
	}
}

func (c *conn) claimClosed() bool {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.engineClosed || c.closePosted {
		return false
	}
	c.closePosted = true
	return true
}

func (c *conn) writeStopped() {
	c.mu.Lock()
	c.unwritten = true
	c.mu.Unlock()
}

func (c *conn) stoppedWriting() bool {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.unwritten
}

func (c *conn) closedByEngine() bool {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.engineClosed
}
