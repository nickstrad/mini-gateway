package fakepg

import (
	"encoding/binary"
	"errors"
	"fmt"
	"strconv"

	"github.com/jackc/pgx/v5/pgproto3"
)

const (
	codeSSLRequest uint32 = 80877103
	codeGSSRequest uint32 = 80877104
	codeCancel     uint32 = 80877102
	protocol30     uint32 = pgproto3.ProtocolVersion30
	oidInt4        uint32 = 23
	oidText        uint32 = 25
	oidBool        uint32 = 16
	oidVoid        uint32 = 2278
)

var errBadStartup = errors.New("fakepg: invalid startup packet")

type startupKind uint8

const (
	startupNormal startupKind = iota + 1
	startupSSL
	startupGSS
	startupCancel
)

type startupPacket struct {
	kind    startupKind
	version uint32
	user    string
	db      string
	appName string
	pid     uint32
	secret  []byte
}

func decodeStartup(body []byte) (startupPacket, error) {
	if len(body) < 4 {
		return startupPacket{}, errBadStartup
	}
	code := binary.BigEndian.Uint32(body)
	switch code {
	case codeSSLRequest:
		return startupPacket{kind: startupSSL}, nil
	case codeGSSRequest:
		return startupPacket{kind: startupGSS}, nil
	case codeCancel:
		var c pgproto3.CancelRequest
		if err := c.Decode(body); err != nil {
			return startupPacket{}, err
		}
		return startupPacket{kind: startupCancel, pid: c.ProcessID, secret: c.SecretKey}, nil
	}
	p := startupPacket{kind: startupNormal, version: code}
	if code != protocol30 {
		return p, nil
	}
	var m pgproto3.StartupMessage
	if err := m.Decode(body); err != nil {
		return p, err
	}
	p.user = m.Parameters["user"]
	p.db = m.Parameters["database"]
	p.appName = m.Parameters["application_name"]
	return p, nil
}

func decodeQuery(body []byte) (string, error) {
	var q pgproto3.Query
	if err := q.Decode(body); err != nil {
		return "", err
	}
	return q.String, nil
}

func enc(dst []byte, m pgproto3.BackendMessage) []byte {
	out, err := m.Encode(dst)
	if err != nil {
		return dst
	}
	return out
}

func encAuthOK(dst []byte) []byte { return enc(dst, &pgproto3.AuthenticationOk{}) }

func encParam(dst []byte, name, value string) []byte {
	return enc(dst, &pgproto3.ParameterStatus{Name: name, Value: value})
}

func encKey(dst []byte, pid uint32, secret []byte) []byte {
	return enc(dst, &pgproto3.BackendKeyData{ProcessID: pid, SecretKey: secret})
}

func encReady(dst []byte, status byte) []byte {
	return enc(dst, &pgproto3.ReadyForQuery{TxStatus: status})
}

func encRowDesc(dst []byte, name string, oid uint32, size int16) []byte {
	return enc(dst, &pgproto3.RowDescription{Fields: []pgproto3.FieldDescription{{
		Name: []byte(name), DataTypeOID: oid, DataTypeSize: size, TypeModifier: -1,
	}}})
}

func encDataRow(dst []byte, v []byte) []byte {
	return enc(dst, &pgproto3.DataRow{Values: [][]byte{v}})
}

func encEmptyRow(dst []byte) []byte { return enc(dst, &pgproto3.DataRow{}) }

func encCommand(dst []byte, tag string) []byte {
	return enc(dst, &pgproto3.CommandComplete{CommandTag: []byte(tag)})
}

func encEmptyQuery(dst []byte) []byte { return enc(dst, &pgproto3.EmptyQueryResponse{}) }

type pgError struct {
	severity string
	code     string
	message  string
}

func (e pgError) Error() string { return e.severity + " " + e.code + ": " + e.message }

func encError(dst []byte, e pgError) []byte {
	return enc(dst, &pgproto3.ErrorResponse{Severity: e.severity, SeverityUnlocalized: e.severity, Code: e.code, Message: e.message})
}

func encNotice(dst []byte, e pgError) []byte {
	return enc(dst, &pgproto3.NoticeResponse{Severity: e.severity, SeverityUnlocalized: e.severity, Code: e.code, Message: e.message})
}

func rowHeaderLen(size int64) int64 { return 1 + 4 + 2 + 4 + size }

func appendRepeatRow(dst []byte, unit string, size int64) []byte {
	dst = append(dst, 'D')
	dst = binary.BigEndian.AppendUint32(dst, uint32(rowHeaderLen(size)-1))
	dst = binary.BigEndian.AppendUint16(dst, 1)
	dst = binary.BigEndian.AppendUint32(dst, uint32(size))
	for i := int64(0); i < size; i++ {
		dst = append(dst, unit[i%int64(len(unit))])
	}
	return dst
}

func errorf(severity, code, format string, args ...any) pgError {
	return pgError{severity: severity, code: code, message: fmt.Sprintf(format, args...)}
}

func CodeValue(code string) int64 {
	v, err := strconv.ParseInt(code, 36, 64)
	if err != nil {
		return -1
	}
	return v
}
