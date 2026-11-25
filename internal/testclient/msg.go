package testclient

import (
	"encoding/binary"
	"fmt"

	"github.com/jackc/pgx/v5/pgproto3"
)

// Wire message type bytes and protocol constants for the simple-query subset.
const (
	MsgAuth          byte   = 'R'
	MsgParamStatus   byte   = 'S'
	MsgBackendKey    byte   = 'K'
	MsgReady         byte   = 'Z'
	MsgRowDesc       byte   = 'T'
	MsgDataRow       byte   = 'D'
	MsgCommand       byte   = 'C'
	MsgError         byte   = 'E'
	MsgNotice        byte   = 'N'
	MsgEmptyQuery    byte   = 'I'
	MsgNegotiate     byte   = 'v'
	MsgSSLAnswer     byte   = 0
	ProtocolVersion  uint32 = pgproto3.ProtocolVersion30
	sslRequestCode   uint32 = 80877103
	gssRequestCode   uint32 = 80877104
	cancelCode       uint32 = 80877102
	startupFixedSize        = 8
)

// Msg is the flattened, protocol-decoded form of a backend message. Only the
// fields corresponding to Type are populated.
type Msg struct {
	Type      byte
	Status    byte
	Severity  string
	Code      string
	Text      string
	Tag       string
	Name      string
	Value     string
	ProcessID uint32
	Secret    []byte
	Auth      uint32
	Fields    []string
	Values    [][]byte
	Data      []byte
}

// MsgName returns a human-readable Postgres message name for a wire type byte.
func MsgName(t byte) string {
	switch t {
	case MsgAuth:
		return "Authentication"
	case MsgParamStatus:
		return "ParameterStatus"
	case MsgBackendKey:
		return "BackendKeyData"
	case MsgReady:
		return "ReadyForQuery"
	case MsgRowDesc:
		return "RowDescription"
	case MsgDataRow:
		return "DataRow"
	case MsgCommand:
		return "CommandComplete"
	case MsgError:
		return "ErrorResponse"
	case MsgNotice:
		return "NoticeResponse"
	case MsgEmptyQuery:
		return "EmptyQueryResponse"
	case MsgNegotiate:
		return "NegotiateProtocolVersion"
	case MsgSSLAnswer:
		return "SSLAnswer"
	}
	return fmt.Sprintf("Unknown(%q)", t)
}

// Decode parses one framed backend message body using pgproto3 message types
// without pgproto3's blocking Frontend reader.
func Decode(typ byte, body []byte) (Msg, error) {
	m := Msg{Type: typ}
	switch typ {
	case MsgAuth:
		if len(body) < 4 {
			return m, fmt.Errorf("testclient: authentication message of %d bytes", len(body))
		}
		m.Auth = binary.BigEndian.Uint32(body)
		m.Data = append([]byte(nil), body[4:]...)
	case MsgParamStatus:
		var p pgproto3.ParameterStatus
		if err := p.Decode(body); err != nil {
			return m, err
		}
		m.Name, m.Value = p.Name, p.Value
	case MsgBackendKey:
		var k pgproto3.BackendKeyData
		if err := k.Decode(body); err != nil {
			return m, err
		}
		m.ProcessID, m.Secret = k.ProcessID, k.SecretKey
	case MsgReady:
		var r pgproto3.ReadyForQuery
		if err := r.Decode(body); err != nil {
			return m, err
		}
		m.Status = r.TxStatus
	case MsgRowDesc:
		var r pgproto3.RowDescription
		if err := r.Decode(body); err != nil {
			return m, err
		}
		for _, f := range r.Fields {
			m.Fields = append(m.Fields, string(f.Name))
		}
	case MsgDataRow:
		var d pgproto3.DataRow
		if err := d.Decode(body); err != nil {
			return m, err
		}
		for _, v := range d.Values {
			m.Values = append(m.Values, append([]byte(nil), v...))
		}
	case MsgCommand:
		var c pgproto3.CommandComplete
		if err := c.Decode(body); err != nil {
			return m, err
		}
		m.Tag = string(c.CommandTag)
	case MsgError, MsgNotice:
		var e pgproto3.ErrorResponse
		if err := e.Decode(body); err != nil {
			return m, err
		}
		m.Severity, m.Code, m.Text = e.Severity, e.Code, e.Message
	case MsgEmptyQuery, MsgNegotiate:
	default:
		return m, fmt.Errorf("testclient: unknown backend message type %q", typ)
	}
	return m, nil
}

// Param is a single name/value startup or server parameter pair.
type Param struct {
	Name  string
	Value string
}

// EncodeStartup appends a protocol 3.0 startup packet with parameters written in
// slice order. Encoded directly rather than via pgproto3.StartupMessage.Encode
// because pgproto3 iterates over a Go map, which is non-deterministic.
func EncodeStartup(dst []byte, params []Param) []byte {
	start := len(dst)
	dst = binary.BigEndian.AppendUint32(dst, 0)
	dst = binary.BigEndian.AppendUint32(dst, ProtocolVersion)
	for _, p := range params {
		dst = append(dst, p.Name...)
		dst = append(dst, 0)
		dst = append(dst, p.Value...)
		dst = append(dst, 0)
	}
	dst = append(dst, 0)
	binary.BigEndian.PutUint32(dst[start:], uint32(len(dst)-start))
	return dst
}

func encodeCode(dst []byte, code uint32) []byte {
	dst = binary.BigEndian.AppendUint32(dst, startupFixedSize)
	return binary.BigEndian.AppendUint32(dst, code)
}

// EncodeSSLRequest appends an 8-byte Postgres SSLRequest packet.
func EncodeSSLRequest(dst []byte) []byte { return encodeCode(dst, sslRequestCode) }

// EncodeGSSRequest appends an 8-byte Postgres GSSENCRequest packet.
func EncodeGSSRequest(dst []byte) []byte { return encodeCode(dst, gssRequestCode) }

// EncodeCancel appends a Postgres CancelRequest packet for the given backend
// process ID and cancel secret.
func EncodeCancel(dst []byte, pid uint32, secret []byte) []byte {
	out, err := (&pgproto3.CancelRequest{ProcessID: pid, SecretKey: secret}).Encode(dst)
	if err != nil {
		return dst
	}
	return out
}

// EncodeQuery appends a simple-protocol Query ('Q') message.
func EncodeQuery(dst []byte, q string) []byte {
	out, err := (&pgproto3.Query{String: q}).Encode(dst)
	if err != nil {
		return dst
	}
	return out
}

// EncodeTerminate appends a frontend Terminate ('X') message.
func EncodeTerminate(dst []byte) []byte {
	out, _ := (&pgproto3.Terminate{}).Encode(dst)
	return out
}
