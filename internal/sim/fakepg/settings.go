package fakepg

import (
	"slices"
	"strings"
)

const MaxSettings = 64

type setting struct {
	name     string
	value    string
	reported bool
	readOnly bool
}

var reportedNames = []string{
	"application_name", "client_encoding", "DateStyle", "default_transaction_read_only", "in_hot_standby",
	"integer_datetimes", "IntervalStyle", "is_superuser", "scram_iterations", "server_encoding",
	"server_version", "session_authorization", "standard_conforming_strings", "TimeZone",
}

func ReportedParameters() []string { return slices.Clone(reportedNames) }

func (srv *Server) defaultSettings(user, appName string) []setting {
	super := "off"
	if allowed(srv.cfg.Superusers, user) && len(srv.cfg.Superusers) > 0 {
		super = "on"
	}
	values := []string{appName, "UTF8", "ISO, MDY", "off", "off", "on", "postgres", super, "4096", "UTF8",
		srv.cfg.ServerVersion, user, "on", "UTC"}
	out := make([]setting, 0, len(reportedNames)+5)
	for i, n := range reportedNames {
		ro := n == "in_hot_standby" || n == "integer_datetimes" || n == "is_superuser" || n == "server_encoding" ||
			n == "server_version" || n == "session_authorization"
		out = append(out, setting{name: n, value: values[i], reported: true, readOnly: ro})
	}
	return append(out,
		setting{name: "idle_in_transaction_session_timeout", value: "0"},
		setting{name: "lock_timeout", value: "0"},
		setting{name: "search_path", value: `"$user", public`},
		setting{name: "statement_timeout", value: "0"},
		setting{name: "work_mem", value: "4MB"},
	)
}

func reportedOf(all []setting) []setting {
	var out []setting
	for _, s := range all {
		if s.reported {
			out = append(out, s)
		}
	}
	return out
}

func findSetting(all []setting, name string) int {
	for i := range all {
		if strings.EqualFold(all[i].name, name) {
			return i
		}
	}
	return -1
}

func displayValue(name, v string) string {
	if strings.HasSuffix(name, "_timeout") && v != "0" && v != "" && strings.Trim(v, "0123456789") == "" {
		return v + "ms"
	}
	return v
}

func (b *backend) saveSettings() {
	if b.tx != nil && b.tx.saved == nil {
		b.tx.saved = slices.Clone(b.settings)
	}
}

func (b *backend) session(st Stmt) *pgError {
	switch st.Kind {
	case KindSet:
		return b.set(st.Name, st.Value)
	case KindReset:
		b.saveSettings()
		defaults := b.srv.defaultSettings(b.user, b.defaultApp())
		if st.Name == "all" {
			b.resetAll(defaults)
		} else {
			i := findSetting(b.settings, st.Name)
			if i < 0 {
				return unknownSetting(st.Name)
			}
			if j := findSetting(defaults, st.Name); j >= 0 {
				b.settings[i].value = defaults[j].value
			}
		}
		b.emit(encCommand(nil, "RESET"))
	case KindShow:
		i := findSetting(b.settings, st.Name)
		if i < 0 {
			return unknownSetting(st.Name)
		}
		b.emit(encCommand(encDataRow(nil, []byte(b.settings[i].value)), "SHOW"))
	case KindDiscard:
		if b.tx != nil {
			return &pgError{severity: "ERROR", code: "25001", message: "DISCARD ALL cannot run inside a transaction block"}
		}
		b.resetAll(b.srv.defaultSettings(b.user, b.defaultApp()))
		b.emit(encCommand(nil, "DISCARD ALL"))
	}
	return nil
}

func (b *backend) defaultApp() string { return b.appName }

func (b *backend) resetAll(defaults []setting) {
	kept := b.settings[:0]
	for _, s := range b.settings {
		if j := findSetting(defaults, s.name); j >= 0 {
			s.value = defaults[j].value
			kept = append(kept, s)
		}
	}
	b.settings = kept
}

func unknownSetting(name string) *pgError {
	return &pgError{severity: "ERROR", code: "42704", message: "unrecognized configuration parameter \"" + name + "\""}
}

func (b *backend) set(name, value string) *pgError {
	i := findSetting(b.settings, name)
	if i < 0 && !strings.Contains(name, ".") {
		return unknownSetting(name)
	}
	if i >= 0 && b.settings[i].readOnly {
		return &pgError{severity: "ERROR", code: "55P02", message: "parameter \"" + name + "\" cannot be changed"}
	}
	b.saveSettings()
	if i < 0 {
		if len(b.settings) >= MaxSettings {
			return &pgError{severity: "ERROR", code: "54000", message: "fakepg: too many settings"}
		}
		b.settings = append(b.settings, setting{name: name})
		i = len(b.settings) - 1
	}
	b.settings[i].value = displayValue(name, value)
	b.emit(encCommand(nil, "SET"))
	return nil
}

func (b *backend) reportChanges() {
	var out []byte
	for i := range b.reported {
		j := findSetting(b.settings, b.reported[i].name)
		v := ""
		if j >= 0 {
			v = b.settings[j].value
		}
		if v != b.reported[i].value {
			b.reported[i].value = v
			b.srv.stats.paramReports++
			out = encParam(out, b.reported[i].name, v)
		}
	}
	if out != nil {
		b.emit(out)
	}
}
