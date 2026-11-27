// Package proxy implements the single-threaded, sans-I/O proxy engine (decision D1).
// The engine takes an explicit virtual or monotonic Instant and a stream of byte
// and lifecycle Events, and emits Actions for a driver (internal/shell on real
// sockets, or internal/proxy/proxysim on simnet) to execute. It never reads a
// clock, starts a goroutine, or touches a socket.
package proxy

import "github.com/nickstrad/mini-gateway/internal/pgwire"

// Instant is a monotonic timestamp in nanoseconds supplied by the driver.
// Zero represents the start of the engine's clock or an unset wake-up deadline.
type Instant int64

// ConnID uniquely identifies a client or database connection within one engine.
// Client IDs are chosen by the driver; database IDs are allocated by the engine.
type ConnID int64

// EventKind identifies the kind of input event delivered to Engine.Step.
type EventKind uint8

const (
	// EvClientAccepted announces a new client connection assigned by the driver.
	EvClientAccepted EventKind = iota + 1
	// EvClientData delivers bytes read from a client; Data is borrowed for Step.
	EvClientData
	// EvClientClosed announces that a client connection has closed or reset.
	EvClientClosed
	// EvClientWrote acknowledges N bytes written to a client socket.
	EvClientWrote
	// EvClientTLS delivers the outcome of a client-leg TLS handshake.
	EvClientTLS
	// EvDBOpened announces that a dial requested by ActDBOpen connected.
	EvDBOpened
	// EvDBDialFailed announces that a dial requested by ActDBOpen failed.
	EvDBDialFailed
	// EvDBData delivers bytes read from a database connection; Data is borrowed for Step.
	EvDBData
	// EvDBClosed announces that a database connection closed. Drivers must post
	// every EvDBWrote and any EvDBWriteRefused before EvDBClosed (rule G8).
	EvDBClosed
	// EvDBWrote acknowledges N bytes accepted by a database socket.
	EvDBWrote
	// EvDBTLS delivers the outcome of a database-leg TLS handshake.
	EvDBTLS
	// EvTimer signals that the wake-up deadline requested by ActWake was reached.
	EvTimer
	// EvPublish requests an observability snapshot publication (ActPublish).
	EvPublish
	// EvDraining toggles draining mode (On=true refuses new work outside a transaction).
	EvDraining
	// EvDBWriteRefused reports that a database write stopped with bytes not accepted
	// at the last acknowledged offset; it must precede EvDBClosed so rule G8 can
	// prove zero bytes of a request were accepted.
	EvDBWriteRefused
	// EvDBCancelDone reports that the secondary cancel connection for Conn finished
	// or hit its clean-up deadline.
	EvDBCancelDone
	numEventKinds
)

var eventNames = [numEventKinds]string{"?", "client-accepted", "client-data", "client-closed", "client-wrote",
	"client-tls", "db-opened", "db-dial-failed", "db-data", "db-closed", "db-wrote", "db-tls", "timer", "publish",
	"draining", "db-write-refused", "db-cancel-done"}

// String returns the canonical hyphenated name of the event kind.
func (k EventKind) String() string {
	if k < numEventKinds {
		return eventNames[k]
	}
	return "?"
}

// Event is one input to Engine.Step. Data slices are borrowed only for the
// duration of the Step call.
type Event struct {
	Kind EventKind
	Conn ConnID
	Data []byte
	N    int
	TLS  pgwire.HandshakeResult
	On   bool
}

// ActionKind identifies the kind of side effect returned by Engine.Start,
// Engine.Step, and Engine.Shutdown for the driver to execute in order.
type ActionKind uint8

const (
	// ActClientWrite queues Data to be written to a client connection.
	ActClientWrite ActionKind = iota + 1
	// ActClientClose closes a client connection after any prior queued writes flush.
	ActClientClose
	// ActClientStartTLS runs the server-side TLS handshake after prior queued writes.
	ActClientStartTLS
	// ActClientPause asks the driver to stop reading from a client connection.
	ActClientPause
	// ActClientResume asks the driver to resume reading from a client connection.
	ActClientResume
	// ActDBOpen asks the driver to dial a database connection with the engine's Conn ID.
	ActDBOpen
	// ActDBWrite queues Data to be written to a database connection.
	ActDBWrite
	// ActDBClose closes a database connection and cancels any dial in progress.
	ActDBClose
	// ActDBStartTLS runs the client-side TLS handshake after prior queued writes.
	ActDBStartTLS
	// ActDBPause asks the driver to stop reading from a database connection (back pressure).
	ActDBPause
	// ActDBResume asks the driver to resume reading from a database connection.
	ActDBResume
	// ActWake arms or cancels (At == 0) the engine's single earliest wake-up timer.
	ActWake
	// ActPublish instructs the driver to publish the current observability snapshot.
	ActPublish
	// ActDBCancel asks the driver to open a short-lived cancel connection with ID
	// Conn, write the CancelRequest in Data, read to EOF or cleanup deadline, and
	// post EvDBCancelDone.
	ActDBCancel
	numActionKinds
)

var actionNames = [numActionKinds]string{"?", "client-write", "client-close", "client-start-tls", "client-pause",
	"client-resume", "db-open", "db-write", "db-close", "db-start-tls", "db-pause", "db-resume", "wake", "publish",
	"db-cancel"}

// String returns the canonical hyphenated name of the action kind.
func (k ActionKind) String() string {
	if k < numActionKinds {
		return actionNames[k]
	}
	return "?"
}

// Action is one driver-executed command returned by the engine. Any Data slice
// is owned by the caller.
type Action struct {
	Kind     ActionKind
	Conn     ConnID
	Data     []byte
	At       Instant
	Reason   CloseReason
	TLSFirst bool
}

const (
	// FirstClientRead is the exact byte count a driver must read from a new client
	// before pausing for ActClientResume or ActClientStartTLS, preventing bytes
	// from being buffered across a TLS upgrade.
	FirstClientRead = 8
	// FirstDBTLSRead is the exact byte count (the single 'S'/'N' SSL answer byte)
	// a driver must read when ActDBOpen sets TLSFirst.
	FirstDBTLSRead = 1
	// MaxRead caps each read chunk delivered to the engine so pause thresholds and
	// per-client reply buffers stay bounded.
	MaxRead = 16 << 10
)
