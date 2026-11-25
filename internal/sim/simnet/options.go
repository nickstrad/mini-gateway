package simnet

import (
	"errors"
	"time"
)

// Chunking controls how accepted writes are split into delivery events on a pipe.
type Chunking uint8

// Supported delivery chunking modes for a Direction.
const (
	// ChunkWhole delivers each accepted Write as a single piece.
	ChunkWhole Chunking = iota
	// ChunkFixed splits writes into pieces of Direction.ChunkSize bytes (down to 1).
	ChunkFixed
	// ChunkRandom splits writes into seeded random pieces, capped at MaxRandomPieces.
	ChunkRandom
)

// Default and maximum bounds for simulated connections and listeners.
const (
	// DefaultWindow is the per-pipe send window used when Options.Window <= 0.
	DefaultWindow = 64 << 10
	// MaxWindow caps Options.Window to keep per-pipe memory bounded.
	MaxWindow = 16 << 20
	// DefaultLatency is the default one-way base latency in DefaultOptions.
	DefaultLatency = 50 * time.Microsecond
	// MaxListeners bounds Listener registrations per Net.
	MaxListeners = 1024
	// MaxConns bounds total dials per Net; connection IDs index the table directly.
	MaxConns = 1 << 20
	// MaxRandomPieces caps how many delivery events one ChunkRandom write creates.
	MaxRandomPieces = 64
)

// Latency models one-way delivery delay as Base + uniform[0, Jitter] on the
// virtual clock, saturating at math.MaxInt64 ns.
type Latency struct {
	Base   time.Duration
	Jitter time.Duration
}

// Direction configures chunking and latency for one pipe (Up or Down).
type Direction struct {
	Chunking  Chunking
	ChunkSize int
	Latency   Latency
}

// Options configures a simulated connection's send window and per-direction
// delivery behaviour.
type Options struct {
	Window int
	Up     Direction
	Down   Direction
}

// DefaultOptions returns a 64 KiB window and 50 µs one-way base latency with
// whole-write delivery in both directions.
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

// Event identifies a state transition delivered to an End's Handler on the
// simulator loop.
type Event uint8

// Connection notification events delivered through Handler.OnEvent.
const (
	// EvConnected fires on the client end after one round trip when accepted.
	EvConnected Event = iota + 1
	// EvRefused fires on the client end after one round trip when refused.
	EvRefused
	// EvReadable fires when delivered bytes or a peer FIN are ready to read.
	EvReadable
	// EvWritable fires once when window space frees after a short write or WaitWritable.
	EvWritable
	// EvReset fires when the connection is aborted or reset.
	EvReset
)

// String returns the lowercase name of ev, or "?" for an unknown value.
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

// Handler receives asynchronous events for an End. Callbacks always run as
// their own loop events, never synchronously inside Write, Read, or Close.
type Handler interface {
	OnEvent(e *End, ev Event)
}

// HandlerFunc adapts an ordinary function to the Handler interface.
type HandlerFunc func(e *End, ev Event)

// OnEvent invokes f(e, ev).
func (f HandlerFunc) OnEvent(e *End, ev Event) { f(e, ev) }

// Errors returned by Net and End operations.
var (
	// ErrNotConnected is returned when reading, writing, or half-closing a
	// client end before EvConnected.
	ErrNotConnected = errors.New("simnet: not connected")
	// ErrClosed is returned after an end has closed the direction being used.
	ErrClosed = errors.New("simnet: closed")
	// ErrReset is returned by operations on a reset connection.
	ErrReset = errors.New("simnet: connection reset")
	// ErrRefused is returned by operations on a refused dial.
	ErrRefused = errors.New("simnet: connection refused")
	// ErrGone is returned by operations on an end silenced or killed by half-open.
	ErrGone = errors.New("simnet: end is gone")
	// ErrTooManyConns fails the simulation if Net.Dial exceeds MaxConns.
	ErrTooManyConns = errors.New("simnet: too many connections")
	// ErrAddrInUse is returned by Listen when an active listener owns the name.
	ErrAddrInUse = errors.New("simnet: address in use")
	// ErrTooManyListen is returned by Listen once MaxListeners have been created.
	ErrTooManyListen = errors.New("simnet: too many listeners")
)

// CloseReason records why a connection transitioned to Done.
type CloseReason uint8

// Terminal close reasons tracked per connection and in ClosedSnapshot.
const (
	// ReasonNone indicates the connection has not finished yet.
	ReasonNone CloseReason = iota
	// ReasonGraceful indicates both ends closed normally.
	ReasonGraceful
	// ReasonReset indicates a local or injected reset ended the connection.
	ReasonReset
	// ReasonHalfOpen indicates the surviving end closed after a half-open fault.
	ReasonHalfOpen
	// ReasonSilent indicates the client closed an accept-then-silence connection.
	ReasonSilent
	// ReasonAborted indicates the client closed or reset while still connecting.
	ReasonAborted
)
