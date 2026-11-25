package pgwire

import "encoding/binary"

// Postgres wire-protocol constants: startup request codes, backend and frontend
// message type bytes, authentication subtypes, TLS answers, ReadyForQuery
// transaction status bytes, and error severities.
const (
	ProtocolVersion30 uint32 = 3<<16 | 0
	CodeSSLRequest    uint32 = 80877103
	CodeGSSRequest    uint32 = 80877104
	CodeCancelRequest uint32 = 80877102

	MsgAuthentication       byte = 'R'
	MsgParameterStatus      byte = 'S'
	MsgBackendKeyData       byte = 'K'
	MsgReadyForQuery        byte = 'Z'
	MsgErrorResponse        byte = 'E'
	MsgNoticeResponse       byte = 'N'
	MsgNotificationResponse byte = 'A'
	MsgNegotiateProtocol    byte = 'v'
	MsgRowDescription       byte = 'T'
	MsgDataRow              byte = 'D'
	MsgCommandComplete      byte = 'C'
	MsgEmptyQueryResponse   byte = 'I'

	MsgQuery        byte = 'Q'
	MsgTerminate    byte = 'X'
	MsgPassword     byte = 'p'
	MsgParse        byte = 'P'
	MsgBind         byte = 'B'
	MsgExecute      byte = 'E'
	MsgDescribe     byte = 'D'
	MsgClose        byte = 'C'
	MsgFlush        byte = 'H'
	MsgSync         byte = 'S'
	MsgFunctionCall byte = 'F'
	MsgCopyData     byte = 'd'
	MsgCopyDone     byte = 'c'
	MsgCopyFail     byte = 'f'

	AuthOK                uint32 = 0
	AuthKerberosV5        uint32 = 2
	AuthCleartextPassword uint32 = 3
	AuthMD5Password       uint32 = 5
	AuthSCMCredential     uint32 = 6
	AuthGSS               uint32 = 7
	AuthGSSContinue       uint32 = 8
	AuthSSPI              uint32 = 9
	AuthSASL              uint32 = 10
	AuthSASLContinue      uint32 = 11
	AuthSASLFinal         uint32 = 12

	AnswerTLSYes byte = 'S'
	AnswerTLSNo  byte = 'N'

	StatusIdle          byte = 'I'
	StatusInTransaction byte = 'T'
	StatusFailed        byte = 'E'

	SeverityFatal = "FATAL"
	SeverityPanic = "PANIC"
	SeverityError = "ERROR"
)

func beginMessage(dst []byte, typ byte) ([]byte, int) {
	dst = append(dst, typ)
	at := len(dst)
	return append(dst, 0, 0, 0, 0), at
}

func finishMessage(dst []byte, at int) []byte {
	binary.BigEndian.PutUint32(dst[at:at+lengthFieldLen], uint32(len(dst)-at))
	return dst
}

func appendCString(dst []byte, s string) []byte {
	return append(append(dst, s...), 0)
}

// AppendAuthentication appends a backend Authentication ('R') message with the
// given auth subtype code and payload data.
func AppendAuthentication(dst []byte, code uint32, data []byte) []byte {
	dst, at := beginMessage(dst, MsgAuthentication)
	dst = binary.BigEndian.AppendUint32(dst, code)
	dst = append(dst, data...)
	return finishMessage(dst, at)
}

// AppendAuthenticationSASL appends an AuthenticationSASL ('R', subtype 10)
// message listing the offered SASL mechanisms terminated by an empty string.
func AppendAuthenticationSASL(dst []byte, mechanisms ...string) []byte {
	dst, at := beginMessage(dst, MsgAuthentication)
	dst = binary.BigEndian.AppendUint32(dst, AuthSASL)
	for _, m := range mechanisms {
		dst = appendCString(dst, m)
	}
	dst = append(dst, 0)
	return finishMessage(dst, at)
}

// AppendParameterStatus appends a backend ParameterStatus ('S') message.
func AppendParameterStatus(dst []byte, name, value string) []byte {
	dst, at := beginMessage(dst, MsgParameterStatus)
	dst = appendCString(dst, name)
	dst = appendCString(dst, value)
	return finishMessage(dst, at)
}

// AppendBackendKeyData appends a backend BackendKeyData ('K') message carrying
// the session's process ID and cancel secret.
func AppendBackendKeyData(dst []byte, k BackendKey) []byte {
	dst, at := beginMessage(dst, MsgBackendKeyData)
	dst = binary.BigEndian.AppendUint32(dst, k.ProcessID)
	dst = append(dst, k.Secret[:]...)
	return finishMessage(dst, at)
}

// AppendReadyForQuery appends a backend ReadyForQuery ('Z') message with the
// transaction status byte ('I', 'T', or 'E').
func AppendReadyForQuery(dst []byte, status byte) []byte {
	dst, at := beginMessage(dst, MsgReadyForQuery)
	dst = append(dst, status)
	return finishMessage(dst, at)
}

// AppendStartupMessage appends an untyped protocol-3.0 StartupMessage with
// params encoded in slice order for deterministic wire bytes.
func AppendStartupMessage(dst []byte, params []Param) []byte {
	at := len(dst)
	dst = append(dst, 0, 0, 0, 0)
	dst = binary.BigEndian.AppendUint32(dst, ProtocolVersion30)
	for _, p := range params {
		dst = appendCString(dst, p.Name)
		dst = appendCString(dst, p.Value)
	}
	dst = append(dst, 0)
	binary.BigEndian.PutUint32(dst[at:at+lengthFieldLen], uint32(len(dst)-at))
	return dst
}

func appendRequestCode(dst []byte, code uint32) []byte {
	dst = binary.BigEndian.AppendUint32(dst, minStartupLength)
	return binary.BigEndian.AppendUint32(dst, code)
}

// AppendSSLRequest appends the 8-byte untyped SSLRequest packet.
func AppendSSLRequest(dst []byte) []byte { return appendRequestCode(dst, CodeSSLRequest) }

// AppendGSSRequest appends the 8-byte untyped GSSENCRequest packet.
func AppendGSSRequest(dst []byte) []byte { return appendRequestCode(dst, CodeGSSRequest) }

// AppendCancelRequest appends the 16-byte untyped CancelRequest packet for k.
func AppendCancelRequest(dst []byte, k BackendKey) []byte {
	dst = binary.BigEndian.AppendUint32(dst, uint32(minStartupLength+lengthFieldLen+len(k.Secret)))
	dst = binary.BigEndian.AppendUint32(dst, CodeCancelRequest)
	dst = binary.BigEndian.AppendUint32(dst, k.ProcessID)
	return append(dst, k.Secret[:]...)
}

// AppendSASLInitialResponse appends a frontend SASLInitialResponse ('p')
// message naming mechanism and carrying the client-first data.
func AppendSASLInitialResponse(dst []byte, mechanism string, data []byte) []byte {
	dst, at := beginMessage(dst, MsgPassword)
	dst = appendCString(dst, mechanism)
	dst = binary.BigEndian.AppendUint32(dst, uint32(len(data)))
	dst = append(dst, data...)
	return finishMessage(dst, at)
}

// AppendSASLResponse appends a frontend SASLResponse ('p') message carrying
// the client-final data.
func AppendSASLResponse(dst []byte, data []byte) []byte {
	dst, at := beginMessage(dst, MsgPassword)
	dst = append(dst, data...)
	return finishMessage(dst, at)
}

// AppendQuery appends a frontend simple Query ('Q') message for sql.
func AppendQuery(dst []byte, sql []byte) []byte {
	dst, at := beginMessage(dst, MsgQuery)
	dst = append(dst, sql...)
	dst = append(dst, 0)
	return finishMessage(dst, at)
}

// AppendTerminate appends a frontend Terminate ('X') message.
func AppendTerminate(dst []byte) []byte {
	dst, at := beginMessage(dst, MsgTerminate)
	return finishMessage(dst, at)
}

// AppendMessageHeader appends the 5-byte typed message header (type + length)
// for a body of bodyLen bytes.
func AppendMessageHeader(dst []byte, typ byte, bodyLen int) []byte {
	dst = append(dst, typ)
	return binary.BigEndian.AppendUint32(dst, uint32(bodyLen+lengthFieldLen))
}
