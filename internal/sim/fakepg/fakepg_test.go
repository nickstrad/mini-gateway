package fakepg_test

import (
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/nickstrad/mini-gateway/internal/sim/fakepg"
	"github.com/nickstrad/mini-gateway/internal/testclient"
)

// Proves startup emits AuthenticationOk, the exact 14 Postgres 17 ParameterStatus names, BackendKeyData, and ReadyForQuery('I').
func TestStartupSequenceMatchesPostgres17(t *testing.T) {
	w := newWorld(t, fakepg.Config{})
	p := w.client()
	want := "connected R " + strings.Repeat("S:x ", 14) + "K ZI"
	got := p.Trace()
	norm := strings.Fields(got)
	for i, f := range norm {
		if strings.HasPrefix(f, "S:") {
			norm[i] = "S:x"
		}
	}
	if strings.Join(norm, " ") != want {
		t.Fatalf("startup: %s", got)
	}
	var names []string
	for _, m := range p.Messages() {
		if m.Type == testclient.MsgParamStatus {
			names = append(names, m.Name)
		}
	}
	if !slices.Equal(names, fakepg.ReportedParameters()) {
		t.Fatalf("parameter names %v", names)
	}
	if p.Session().ProcessID() == 0 || len(p.Session().Secret()) != 4 {
		t.Fatal("no key data")
	}
	w.done(p)
	snap := w.srv.Snapshot()
	if snap.StartupsCompleted != 1 || snap.ConnsAccepted != 1 || snap.ConnsClosed != 1 || snap.Sessions.Value != 0 {
		t.Fatalf("%+v", snap)
	}
}

// Proves SSLRequest and GSSENCRequest are each answered with 'N' before completing normal startup.
func TestEncryptionRequestsAnsweredN(t *testing.T) {
	w := newWorld(t, fakepg.Config{})
	p := w.dial()
	p.PreStartup(true, true)
	p.Startup("app", "")
	w.run(time.Millisecond)
	if got := p.Trace(); !strings.HasPrefix(got, "connected ssl:N ssl:N R ") || !strings.HasSuffix(got, "K ZI") {
		t.Fatalf("got %s", got)
	}
	w.done(p)
}

type statusCase struct {
	name string
	msgs [][]string
	want []string
}

func statusCases() []statusCase {
	return []statusCase{
		{"B1 begin and select in one message", [][]string{{"BEGIN; SELECT 1"}}, []string{"C:BEGIN T D C:SELECT 1 ZT"}},
		{"B2 error inside a transaction then rollback", [][]string{{"BEGIN"}, {"SELECT 1/0"}, {"ROLLBACK"}},
			[]string{"C:BEGIN ZT", "E:22012 ZE", "C:ROLLBACK ZI"}},
		{"B3 two result sets", [][]string{{"SELECT 1; SELECT 2"}}, []string{"T D C:SELECT 1 T D C:SELECT 1 ZI"}},
		{"B4 pipelined with a failure in the middle", [][]string{{"SELECT 'a'", "SELECT 1/0", "SELECT 'c'"}},
			[]string{"T D C:SELECT 1 ZI E:22012 ZI T D C:SELECT 1 ZI"}},
		{"B5 pipelined after begin", [][]string{{"BEGIN", "SELECT txid_current() IS NOT NULL"}, {"ROLLBACK"}},
			[]string{"C:BEGIN ZT T D C:SELECT 1 ZT", "C:ROLLBACK ZI"}},
		{"B7 empty query", [][]string{{""}}, []string{"I ZI"}},
		{"only semicolons", [][]string{{" ; ;"}}, []string{"I ZI"}},
		{"error aborts the rest of the message", [][]string{{"SELECT 1; SELECT 1/0; SELECT 2"}}, []string{"T D C:SELECT 1 E:22012 ZI"}},
		{"aborted transaction ignores statements", [][]string{{"BEGIN"}, {"SELECT x"}, {"SELECT 1"}, {"COMMIT"}},
			[]string{"C:BEGIN ZT", "E:42601 ZE", "E:25P02 ZE", "C:ROLLBACK ZI"}},
		{"start transaction, end", [][]string{{"START TRANSACTION"}, {"END"}}, []string{"C:BEGIN ZT", "C:COMMIT ZI"}},
		{"begin work, abort", [][]string{{"BEGIN WORK"}, {"ABORT"}}, []string{"C:BEGIN ZT", "C:ROLLBACK ZI"}},
		{"commit outside a transaction warns", [][]string{{"COMMIT"}}, []string{"N:25P01 C:COMMIT ZI"}},
		{"rollback outside a transaction warns", [][]string{{"ROLLBACK"}}, []string{"N:25P01 C:ROLLBACK ZI"}},
		{"begin twice warns", [][]string{{"BEGIN"}, {"BEGIN"}, {"COMMIT"}}, []string{"C:BEGIN ZT", "N:25001 C:BEGIN ZT", "C:COMMIT ZI"}},
		{"begin then commit in one message", [][]string{{"BEGIN; SELECT 1; COMMIT"}}, []string{"C:BEGIN T D C:SELECT 1 C:COMMIT ZI"}},
		{"error after begin in one message", [][]string{{"BEGIN; SELECT 1/0; SELECT 1"}, {"ROLLBACK"}}, []string{"C:BEGIN E:22012 ZE", "C:ROLLBACK ZI"}},
		{"unknown statement", [][]string{{"FROBNICATE"}}, []string{"E:42601 ZI"}},
		{"discard all inside a transaction fails", [][]string{{"BEGIN"}, {"DISCARD ALL"}, {"ROLLBACK"}}, []string{"C:BEGIN ZT", "E:25001 ZE", "C:ROLLBACK ZI"}},
	}
}

func runStatusCase(t *testing.T, cfg fakepg.Config, tc statusCase) []string {
	w := newWorld(t, cfg)
	p := w.client()
	var got []string
	for _, m := range tc.msgs {
		got = append(got, w.exchange(p, m...))
	}
	w.done(p)
	return got
}

// Proves ReadyForQuery transaction status bytes match Postgres across 18 single, pipelined, multi-statement, and error sequences.
func TestTransactionStatusTable(t *testing.T) {
	for _, tc := range statusCases() {
		t.Run(tc.name, func(t *testing.T) {
			if got := runStatusCase(t, fakepg.Config{}, tc); !slices.Equal(got, tc.want) {
				t.Fatalf("got  %q\nwant %q", got, tc.want)
			}
		})
	}
}

// Proves the transaction-status table catches a planted bug that reports 'I' inside an open transaction.
func TestPlantedBugIdleInTransactionIsCaughtByTheStatusTable(t *testing.T) {
	cfg := fakepg.Config{}
	fakepg.PlantIdleInTx(&cfg)
	caught := 0
	for _, tc := range statusCases() {
		if got := runStatusCase(t, cfg, tc); !slices.Equal(got, tc.want) {
			caught++
		}
	}
	if caught == 0 {
		t.Fatal("the transaction status table did not catch a fake that reports I inside a transaction")
	}
}

// Proves SET/SHOW/RESET/DISCARD ALL persist session state, emit unsolicited ParameterStatus for reported settings, and roll back with failed transactions.
func TestSessionStateAndParameterStatus(t *testing.T) {
	w := newWorld(t, fakepg.Config{})
	p := w.client()
	got := []string{
		w.exchange(p, "SET statement_timeout = 1234", "SET application_name = 'tenant-a'", "SHOW statement_timeout"),
		w.exchange(p, "DISCARD ALL", "SHOW statement_timeout"),
	}
	want := []string{"C:SET ZI C:SET S:application_name ZI T D C:SHOW ZI", "C:DISCARD ALL S:application_name ZI T D C:SHOW ZI"}
	if !slices.Equal(got, want) {
		t.Fatalf("got %q", got)
	}
	var shown []string
	for _, m := range p.Messages() {
		if m.Type == testclient.MsgDataRow {
			shown = append(shown, string(m.Values[0]))
		}
	}
	if !slices.Equal(shown, []string{"1234ms", "0"}) {
		t.Fatalf("shown %q", shown)
	}
	if got := w.exchange(p, "BEGIN; SET application_name = 'x'; ROLLBACK", "SHOW application_name"); got != "C:BEGIN C:SET C:ROLLBACK ZI T D C:SHOW ZI" {
		t.Fatalf("set inside a rolled back transaction: %s", got)
	}
	if got := w.exchange(p, "SET nonsense = 1", "SHOW nonsense", "SET server_version = '1'", "RESET ALL"); got != "E:42704 ZI E:42704 ZI E:55P02 ZI C:RESET ZI" {
		t.Fatalf("got %s", got)
	}
	w.done(p)
}
