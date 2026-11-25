package pgwire

import (
	"encoding/binary"
	"strings"
)

// MaxParams caps startup parameters on the client side and collected
// ParameterStatus entries on the database side.
const MaxParams = 64

// Standard Postgres startup and runtime parameter names inspected during login.
const (
	ParamUser                 = "user"
	ParamDatabase             = "database"
	ParamApplicationName      = "application_name"
	ParamSessionAuthorization = "session_authorization"
	ParamIsSuperuser          = "is_superuser"
	ParamClientEncoding       = "client_encoding"
)

// Param is a single name/value pair from a StartupMessage or ParameterStatus.
type Param struct {
	Name  string
	Value string
}

// DefaultServerParams returns the 14 ParameterStatus defaults matching Postgres
// 17, used until the pool captures live parameters from its first backend login.
func DefaultServerParams() []Param {
	return []Param{
		{"application_name", ""},
		{"client_encoding", "UTF8"},
		{"DateStyle", "ISO, MDY"},
		{"default_transaction_read_only", "off"},
		{"in_hot_standby", "off"},
		{"integer_datetimes", "on"},
		{"IntervalStyle", "postgres"},
		{"is_superuser", "off"},
		{"scram_iterations", "4096"},
		{"server_encoding", "UTF8"},
		{"server_version", "17.0"},
		{"session_authorization", ""},
		{"standard_conforming_strings", "on"},
		{"TimeZone", "UTC"},
	}
}

// DefaultSessionSettings returns the default fixed session settings permitted
// in a client StartupMessage and sent on backend startup.
func DefaultSessionSettings() []Param {
	return []Param{{ParamClientEncoding, "UTF8"}}
}

// LookupParam finds name in ps case-insensitively and returns its value.
func LookupParam(ps []Param, name string) (string, bool) {
	for _, p := range ps {
		if strings.EqualFold(p.Name, name) {
			return p.Value, true
		}
	}
	return "", false
}

func setParam(ps []Param, name, value string) ([]Param, bool) {
	for i := range ps {
		if ps[i].Name == name {
			ps[i].Value = value
			return ps, true
		}
	}
	if len(ps) >= MaxParams {
		return ps, false
	}
	return append(ps, Param{Name: name, Value: value}), true
}

type startupKind uint8

const (
	startupBad startupKind = iota
	startupNormal
	startupSSL
	startupGSS
	startupCancel
)

type startupPacket struct {
	kind    startupKind
	version uint32
	params  []Param
	key     BackendKey
}

const cancelBodyLen = 12

func parseStartupPacket(body []byte) startupPacket {
	if len(body) < lengthFieldLen {
		return startupPacket{}
	}
	code := binary.BigEndian.Uint32(body)
	rest := body[lengthFieldLen:]
	switch code {
	case CodeSSLRequest:
		if len(rest) != 0 {
			return startupPacket{}
		}
		return startupPacket{kind: startupSSL}
	case CodeGSSRequest:
		if len(rest) != 0 {
			return startupPacket{}
		}
		return startupPacket{kind: startupGSS}
	case CodeCancelRequest:
		if len(body) != cancelBodyLen {
			return startupPacket{}
		}
		var k BackendKey
		k.ProcessID = binary.BigEndian.Uint32(rest)
		copy(k.Secret[:], rest[lengthFieldLen:])
		return startupPacket{kind: startupCancel, key: k}
	}
	p := startupPacket{kind: startupNormal, version: code}
	// Major versions other than 3 do not need parameter parsing; return the
	// version so the caller emits Postgres's protocol-version refusal.
	if code>>16 != ProtocolVersion30>>16 {
		return p
	}
	params, ok := parseStartupParams(rest)
	if !ok {
		return startupPacket{}
	}
	p.params = params
	return p
}

func parseStartupParams(b []byte) ([]Param, bool) {
	var out []Param
	for {
		if len(b) == 0 {
			return nil, false
		}
		if b[0] == 0 {
			return out, len(b) == 1
		}
		name, rest, ok := cutCString(b)
		if !ok {
			return nil, false
		}
		value, rest2, ok := cutCString(rest)
		if !ok || len(out) >= MaxParams {
			return nil, false
		}
		// Match Postgres's last-value-wins semantics for repeated parameter names
		// without letting duplicates bypass MaxParams.
		replaced := false
		for i := range out {
			if out[i].Name == name {
				out[i].Value, replaced = value, true
			}
		}
		if !replaced {
			out = append(out, Param{Name: name, Value: value})
		}
		b = rest2
	}
}

// ParseStartupParams parses the NUL-terminated key/value pairs following the
// protocol version word in a StartupMessage.
func ParseStartupParams(b []byte) ([]Param, bool) { return parseStartupParams(b) }
