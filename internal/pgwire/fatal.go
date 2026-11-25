package pgwire

import "encoding/binary"

const (
	fieldSeverity            byte = 'S'
	fieldSeverityUnlocalized byte = 'V'
	fieldCode                byte = 'C'
	fieldMessage             byte = 'M'
)

const (
	CodeInvalidAuthorization = "28000"
	CodeInvalidPassword      = "28P01"
	CodeFeatureNotSupported  = "0A000"
	CodeUnknownDatabase      = "3D000"
	CodeProtocolViolation    = "08P01"
	CodeTooManyConnections   = "53300"
	CodeConnectionFailure    = "08006"
)

func AppendErrorResponse(dst []byte, severity, code, message string) []byte {
	dst, at := beginMessage(dst, MsgErrorResponse)
	dst = append(dst, fieldSeverity)
	dst = appendCString(dst, severity)
	dst = append(dst, fieldSeverityUnlocalized)
	dst = appendCString(dst, severity)
	dst = append(dst, fieldCode)
	dst = appendCString(dst, code)
	dst = append(dst, fieldMessage)
	dst = appendCString(dst, message)
	dst = append(dst, 0)
	return finishMessage(dst, at)
}

func AppendLegacyFatal(dst []byte, message string) []byte {
	dst = append(dst, MsgErrorResponse)
	return appendCString(dst, SeverityFatal+":  "+message+"\n")
}

func AppendNegotiateProtocolVersion(dst []byte, newest uint32, unrecognized []string) []byte {
	dst, at := beginMessage(dst, MsgNegotiateProtocol)
	dst = binary.BigEndian.AppendUint32(dst, newest)
	dst = binary.BigEndian.AppendUint32(dst, uint32(len(unrecognized)))
	for _, u := range unrecognized {
		dst = appendCString(dst, u)
	}
	return finishMessage(dst, at)
}

func AppendFatal(dst []byte, code, message string) []byte {
	return AppendErrorResponse(dst, SeverityFatal, code, message)
}

type ErrorFields struct {
	Severity string
	Code     string
	Message  string
}

func (e ErrorFields) Fatal() bool { return e.Severity == SeverityFatal || e.Severity == SeverityPanic }

func ParseErrorFields(body []byte) (ErrorFields, bool) {
	var e ErrorFields
	var localized string
	for len(body) > 0 {
		f := body[0]
		if f == 0 {
			if e.Severity == "" {
				e.Severity = localized
			}
			return e, len(body) == 1
		}
		v, rest, ok := cutCString(body[1:])
		if !ok {
			return e, false
		}
		switch f {
		case fieldSeverity:
			localized = v
		case fieldSeverityUnlocalized:
			e.Severity = v
		case fieldCode:
			e.Code = v
		case fieldMessage:
			e.Message = v
		}
		body = rest
	}
	return e, false
}

func cutCString(b []byte) (string, []byte, bool) {
	for i, c := range b {
		if c == 0 {
			return string(b[:i]), b[i+1:], true
		}
	}
	return "", nil, false
}
