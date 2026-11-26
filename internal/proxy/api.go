package proxy

import "github.com/nickstrad/mini-gateway/internal/pgwire"

type Instant int64

type ConnID int64

type EventKind uint8

const (
	EvClientAccepted EventKind = iota + 1
	EvClientData
	EvClientClosed
	EvClientWrote
	EvClientTLS
	EvDBOpened
	EvDBDialFailed
	EvDBData
	EvDBClosed
	EvDBWrote
	EvDBTLS
	EvTimer
	EvPublish
	EvDraining
	numEventKinds
)

var eventNames = [numEventKinds]string{"?", "client-accepted", "client-data", "client-closed", "client-wrote",
	"client-tls", "db-opened", "db-dial-failed", "db-data", "db-closed", "db-wrote", "db-tls", "timer", "publish",
	"draining"}

func (k EventKind) String() string {
	if k < numEventKinds {
		return eventNames[k]
	}
	return "?"
}

type Event struct {
	Kind EventKind
	Conn ConnID
	Data []byte
	N    int
	TLS  pgwire.HandshakeResult
	On   bool
}

type ActionKind uint8

const (
	ActClientWrite ActionKind = iota + 1
	ActClientClose
	ActClientStartTLS
	ActClientPause
	ActClientResume
	ActDBOpen
	ActDBWrite
	ActDBClose
	ActDBStartTLS
	ActDBPause
	ActDBResume
	ActWake
	ActPublish
	numActionKinds
)

var actionNames = [numActionKinds]string{"?", "client-write", "client-close", "client-start-tls", "client-pause",
	"client-resume", "db-open", "db-write", "db-close", "db-start-tls", "db-pause", "db-resume", "wake", "publish"}

func (k ActionKind) String() string {
	if k < numActionKinds {
		return actionNames[k]
	}
	return "?"
}

type Action struct {
	Kind     ActionKind
	Conn     ConnID
	Data     []byte
	At       Instant
	Reason   CloseReason
	TLSFirst bool
}

const (
	FirstClientRead = 8
	FirstDBTLSRead  = 1
	MaxRead         = 16 << 10
)
