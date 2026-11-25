package pgwire

import "encoding/binary"

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

func AppendAuthentication(dst []byte, code uint32, data []byte) []byte {
	dst, at := beginMessage(dst, MsgAuthentication)
	dst = binary.BigEndian.AppendUint32(dst, code)
	dst = append(dst, data...)
	return finishMessage(dst, at)
}

func AppendAuthenticationSASL(dst []byte, mechanisms ...string) []byte {
	dst, at := beginMessage(dst, MsgAuthentication)
	dst = binary.BigEndian.AppendUint32(dst, AuthSASL)
	for _, m := range mechanisms {
		dst = appendCString(dst, m)
	}
	dst = append(dst, 0)
	return finishMessage(dst, at)
}

func AppendParameterStatus(dst []byte, name, value string) []byte {
	dst, at := beginMessage(dst, MsgParameterStatus)
	dst = appendCString(dst, name)
	dst = appendCString(dst, value)
	return finishMessage(dst, at)
}

func AppendBackendKeyData(dst []byte, k BackendKey) []byte {
	dst, at := beginMessage(dst, MsgBackendKeyData)
	dst = binary.BigEndian.AppendUint32(dst, k.ProcessID)
	dst = append(dst, k.Secret[:]...)
	return finishMessage(dst, at)
}

func AppendReadyForQuery(dst []byte, status byte) []byte {
	dst, at := beginMessage(dst, MsgReadyForQuery)
	dst = append(dst, status)
	return finishMessage(dst, at)
}

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

func AppendSSLRequest(dst []byte) []byte { return appendRequestCode(dst, CodeSSLRequest) }

func AppendGSSRequest(dst []byte) []byte { return appendRequestCode(dst, CodeGSSRequest) }

func AppendCancelRequest(dst []byte, k BackendKey) []byte {
	dst = binary.BigEndian.AppendUint32(dst, uint32(minStartupLength+lengthFieldLen+len(k.Secret)))
	dst = binary.BigEndian.AppendUint32(dst, CodeCancelRequest)
	dst = binary.BigEndian.AppendUint32(dst, k.ProcessID)
	return append(dst, k.Secret[:]...)
}

func AppendSASLInitialResponse(dst []byte, mechanism string, data []byte) []byte {
	dst, at := beginMessage(dst, MsgPassword)
	dst = appendCString(dst, mechanism)
	dst = binary.BigEndian.AppendUint32(dst, uint32(len(data)))
	dst = append(dst, data...)
	return finishMessage(dst, at)
}

func AppendSASLResponse(dst []byte, data []byte) []byte {
	dst, at := beginMessage(dst, MsgPassword)
	dst = append(dst, data...)
	return finishMessage(dst, at)
}

func AppendQuery(dst []byte, sql []byte) []byte {
	dst, at := beginMessage(dst, MsgQuery)
	dst = append(dst, sql...)
	dst = append(dst, 0)
	return finishMessage(dst, at)
}

func AppendTerminate(dst []byte) []byte {
	dst, at := beginMessage(dst, MsgTerminate)
	return finishMessage(dst, at)
}

func AppendMessageHeader(dst []byte, typ byte, bodyLen int) []byte {
	dst = append(dst, typ)
	return binary.BigEndian.AppendUint32(dst, uint32(bodyLen+lengthFieldLen))
}
