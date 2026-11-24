package simnet

import (
	"errors"
	"time"
)

type Chunking uint8

const (
	ChunkWhole Chunking = iota
	ChunkFixed
	ChunkRandom
)

const (
	DefaultWindow   = 64 << 10
	MaxWindow       = 16 << 20
	DefaultLatency  = 50 * time.Microsecond
	MaxListeners    = 1024
	MaxConns        = 1 << 20
	MaxRandomPieces = 64
)

type Latency struct {
	Base   time.Duration
	Jitter time.Duration
}

type Direction struct {
	Chunking  Chunking
	ChunkSize int
	Latency   Latency
}

type Options struct {
	Window int
	Up     Direction
	Down   Direction
}

func DefaultOptions() Options {
	return Options{
		Window: DefaultWindow,
		Up:     Direction{Latency: Latency{Base: DefaultLatency}},
		Down:   Direction{Latency: Latency{Base: DefaultLatency}},
	}
}

func (o Options) normalized() Options {
	if o.Window <= 0 {
		o.Window = DefaultWindow
	}
	if o.Window > MaxWindow {
		o.Window = MaxWindow
	}
	o.Up = o.Up.normalized()
	o.Down = o.Down.normalized()
	return o
}

func (d Direction) normalized() Direction {
	if d.ChunkSize <= 0 {
		d.ChunkSize = 1
	}
	if d.Latency.Base < 0 {
		d.Latency.Base = 0
	}
	if d.Latency.Jitter < 0 {
		d.Latency.Jitter = 0
	}
	return d
}

type Event uint8

const (
	EvConnected Event = iota + 1
	EvRefused
	EvReadable
	EvWritable
	EvReset
)

func (ev Event) String() string {
	switch ev {
	case EvConnected:
		return "connected"
	case EvRefused:
		return "refused"
	case EvReadable:
		return "readable"
	case EvWritable:
		return "writable"
	case EvReset:
		return "reset"
	}
	return "?"
}

type Handler interface {
	OnEvent(e *End, ev Event)
}

type HandlerFunc func(e *End, ev Event)

func (f HandlerFunc) OnEvent(e *End, ev Event) { f(e, ev) }

var (
	ErrNotConnected  = errors.New("simnet: not connected")
	ErrClosed        = errors.New("simnet: closed")
	ErrReset         = errors.New("simnet: connection reset")
	ErrRefused       = errors.New("simnet: connection refused")
	ErrGone          = errors.New("simnet: end is gone")
	ErrTooManyConns  = errors.New("simnet: too many connections")
	ErrAddrInUse     = errors.New("simnet: address in use")
	ErrTooManyListen = errors.New("simnet: too many listeners")
)

type CloseReason uint8

const (
	ReasonNone CloseReason = iota
	ReasonGraceful
	ReasonReset
	ReasonHalfOpen
	ReasonSilent
	ReasonAborted
)
