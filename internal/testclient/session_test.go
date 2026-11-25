package testclient

import (
	"testing"

	"github.com/jackc/pgx/v5/pgproto3"
)

func backend(msgs ...pgproto3.BackendMessage) []byte {
	var out []byte
	for _, m := range msgs {
		out, _ = m.Encode(out)
	}
	return out
}

func collect(data []byte, piece int, ssl bool) ([]Msg, error) {
	var s Session
	s.MaxMessage = 1 << 16
	s.PreStartup(nil, ssl, false)
	s.Startup(nil, nil)
	var out []Msg
	for i := 0; i < len(data); {
		n := min(piece, len(data)-i)
		s.Feed(data[i : i+n])
		i += n
		for {
			m, ok, err := s.Next()
			if ok {
				out = append(out, m)
				continue
			}
			if err != nil {
				return out, err
			}
			break
		}
	}
	return out, nil
}

func TestSessionTracksStatusKeyAndParameters(t *testing.T) {
	data := backend(&pgproto3.AuthenticationOk{}, &pgproto3.ParameterStatus{Name: "a", Value: "1"},
		&pgproto3.BackendKeyData{ProcessID: 7, SecretKey: []byte{1, 2, 3, 4}}, &pgproto3.ReadyForQuery{TxStatus: 'I'})
	var s Session
	s.Startup(nil, []Param{{Name: "user", Value: "u"}})
	for i := range data {
		s.Feed(data[i : i+1])
		for {
			_, ok, err := s.Next()
			if err != nil {
				t.Fatal(err)
			}
			if !ok {
				break
			}
		}
	}
	if s.Phase() != PhaseReady || s.Status() != 'I' || s.ProcessID() != 7 || len(s.Params()) != 1 {
		t.Fatalf("phase %d status %q pid %d params %v", s.Phase(), s.Status(), s.ProcessID(), s.Params())
	}
	s.Query(nil, "BEGIN")
	s.Feed(backend(&pgproto3.CommandComplete{CommandTag: []byte("BEGIN")}, &pgproto3.ReadyForQuery{TxStatus: 'T'}))
	for {
		_, ok, _ := s.Next()
		if !ok {
			break
		}
	}
	if s.Status() != 'T' || s.Pending() != 0 {
		t.Fatalf("status %q pending %d", s.Status(), s.Pending())
	}
	s.Feed(backend(&pgproto3.ErrorResponse{Severity: "FATAL", Code: "57P01"}))
	if m, ok, _ := s.Next(); !ok || m.Code != "57P01" {
		t.Fatal("fatal message not returned")
	}
	if _, _, err := s.Next(); err == nil || s.Phase() != PhaseClosed {
		t.Fatal("a fatal error must close the session")
	}
}

func TestFramerRejectsImpossibleLengths(t *testing.T) {
	for _, b := range [][]byte{{'Z', 0, 0, 0, 3}, {'D', 0x7f, 0xff, 0xff, 0xff}} {
		var s Session
		s.Startup(nil, nil)
		s.Feed(b)
		if _, _, err := s.Next(); err == nil {
			t.Fatalf("%v accepted", b)
		}
	}
	var s Session
	s.PreStartup(nil, true, false)
	s.Feed([]byte{'X'})
	if _, _, err := s.Next(); err == nil {
		t.Fatal("bad SSL answer accepted")
	}
}

func FuzzSession(f *testing.F) {
	f.Add(backend(&pgproto3.AuthenticationOk{}, &pgproto3.ReadyForQuery{TxStatus: 'I'}), false)
	f.Add(append([]byte{'N'}, backend(&pgproto3.ErrorResponse{Severity: "FATAL", Code: "53300"})...), true)
	f.Add(backend(&pgproto3.RowDescription{Fields: []pgproto3.FieldDescription{{Name: []byte("v")}}}, &pgproto3.DataRow{Values: [][]byte{[]byte("x")}}), false)
	f.Add([]byte("HTTP/1.1 400 Bad Request\r\n\r\n"), false)
	f.Fuzz(func(t *testing.T, data []byte, ssl bool) {
		a, errA := collect(data, len(data)+1, ssl)
		b, errB := collect(data, 1, ssl)
		if len(a) != len(b) || (errA == nil) != (errB == nil) {
			t.Fatalf("chunking changed the result: %d/%v against %d/%v", len(a), errA, len(b), errB)
		}
		for i := range a {
			if a[i].Type != b[i].Type || a[i].Status != b[i].Status || a[i].Code != b[i].Code {
				t.Fatalf("message %d differs", i)
			}
		}
	})
}
