package e2e

import (
	"slices"
	"strings"
	"time"

	"github.com/nickstrad/mini-gateway/internal/pgwire"
	"github.com/nickstrad/mini-gateway/internal/proxy"
	"github.com/nickstrad/mini-gateway/internal/sim/fakepg"
)

var (
	okSelect1 = []string{"T", "D:1", "C:SELECT 1", "ZI"}
	rejected  = []string{"E:ERROR:0A000", "ZI"}
)

func errTokens(code string) []string { return []string{"E:ERROR:" + code, "ZI"} }

func selectN(n string) []string { return []string{"T", "D:" + n, "C:SELECT 1", "ZI"} }

func readOK(v string) []string { return []string{"T", "D:" + v, "C:SELECT 1", "ZI"} }

func step(r *SimRun, client, i int) []string {
	if client >= len(r.Clients) || i >= len(r.Clients[client].Steps) {
		return nil
	}
	return r.Clients[client].Steps[i]
}

func want(r *SimRun, client, i int, exp []string) error {
	if got := step(r, client, i); !slices.Equal(got, exp) {
		return failf("client %d step %d: got %v, want %v", client, i, got, exp)
	}
	return nil
}

func allSteps(r *SimRun, client int) error {
	got, exp := len(r.Clients[client].Steps), len(r.Spec.Clients[client].Steps)
	if got != exp {
		return failf("client %d ran %d of %d steps: %s", client, got, exp, joinSteps(r.Clients[client]))
	}
	for i, s := range r.Clients[client].Steps {
		if len(s) == 0 || !strings.HasPrefix(s[len(s)-1], "Z") {
			return failf("client %d step %d has no end marker: %v", client, i, s)
		}
	}
	return nil
}

func everyClientFinished(r *SimRun) error {
	for c := range r.Clients {
		if err := allSteps(r, c); err != nil {
			return err
		}
	}
	return nil
}

func checkResults(r *SimRun) error {
	if err := everyClientFinished(r); err != nil {
		return err
	}
	direct, err := RunSim(r.Spec, r.Seed, SimOptions{Direct: true})
	if err != nil {
		return err
	}
	if err := ResultsEqual(direct.Clients, r.Clients); err != nil {
		return err
	}
	for c := range r.Clients {
		if err := want(r, c, 12, []string{"T", "D:7", "C:SELECT 1", "ZI"}); err != nil {
			return err
		}
		if err := want(r, c, 11, errTokens("22012")); err != nil {
			return err
		}
	}
	if r.Engine.ResetsCompleted == 0 || r.Engine.Completed == 0 {
		return failf("counters did not move: %+v", r.Engine.ResetsCompleted)
	}
	return nil
}

func ResultsEqual(direct, proxied []ClientResult) error {
	if len(direct) != len(proxied) {
		return failf("%d direct clients, %d through the proxy", len(direct), len(proxied))
	}
	for c := range direct {
		if !slices.EqualFunc(direct[c].Steps, proxied[c].Steps, slices.Equal) {
			return failf("client %d differs:\n direct %s\n proxy  %s", c, joinSteps(direct[c]), joinSteps(proxied[c]))
		}
	}
	return nil
}

func TxCrossings(log []fakepg.StatementRecord) int {
	owner := map[int64]string{}
	n := 0
	for _, s := range log {
		if s.Tag == "" || s.TxID == 0 {
			continue
		}
		o, ok := owner[s.TxID]
		if !ok {
			owner[s.TxID] = s.Tag
			continue
		}
		if o != s.Tag {
			n++
		}
	}
	return n
}

func checkTxIntegrity(r *SimRun) error {
	if d := r.World.Fake.Snapshot(); d.StatementLogDropped != 0 {
		return failf("statement log dropped %d entries", d.StatementLogDropped)
	}
	if n := TxCrossings(r.World.Fake.StatementLog()); n != 0 {
		return failf("%d statements ran inside another client's transaction", n)
	}
	if err := everyClientFinished(r); err != nil {
		return err
	}
	for c := range r.Clients {
		for i := 4; i < len(r.Clients[c].Steps); i += 6 {
			if err := want(r, c, i, []string{"C:COMMIT", "ZI"}); err != nil {
				return err
			}
		}
	}
	if r.Engine.ClientsInTx.Peak < 2 || r.Engine.Queued.Peak == 0 {
		return failf("no interleaving: in-transaction peak %d, queue peak %d", r.Engine.ClientsInTx.Peak, r.Engine.Queued.Peak)
	}
	return nil
}

func checkRejects(r *SimRun) error {
	if err := everyClientFinished(r); err != nil {
		return err
	}
	selects := 0
	for i := 0; i < len(r.Clients[0].Steps); i += 2 {
		if err := want(r, 0, i, rejected); err != nil {
			return err
		}
		if err := want(r, 0, i+1, okSelect1); err != nil {
			return err
		}
		selects++
	}
	for _, i := range []int{0, 2} {
		if err := want(r, 1, i, rejected); err != nil {
			return err
		}
	}
	if err := want(r, 1, 1, okSelect1); err != nil {
		return err
	}
	if err := want(r, 1, 3, selectN("2")); err != nil {
		return err
	}
	selects += 2
	started := int64(0)
	for _, s := range r.World.Fake.StatementLog() {
		if s.Kind != fakepg.KindDiscard {
			started++
		}
	}
	if started != int64(selects) {
		return failf("the database ran %d client statements, want only the %d SELECTs", started, selects)
	}
	if r.Engine.ByRow[pgwire.ErrRowUnsupportedFeature] == 0 || r.Engine.ByRow[pgwire.ErrRowUnsupportedExtended] != 2 {
		return failf("reject counters %d / %d", r.Engine.ByRow[pgwire.ErrRowUnsupportedFeature], r.Engine.ByRow[pgwire.ErrRowUnsupportedExtended])
	}
	return nil
}

type settingSet map[string]string

func parseSet(text string) (string, string, bool) {
	f := strings.Fields(strings.ToLower(text))
	if len(f) > 0 && f[0] == "set" {
		f = f[1:]
	}
	if len(f) > 0 && f[0] == "session" {
		f = f[1:]
	}
	if len(f) != 3 || (f[1] != "=" && f[1] != "to") {
		return "", "", false
	}
	return f[0], strings.Trim(f[2], "'"), true
}

func stripTag(text string) string {
	if i := strings.Index(text, "*/"); strings.HasPrefix(text, "/*") && i >= 0 {
		return strings.TrimSpace(text[i+2:])
	}
	return text
}

func SessionCrossings(r *SimRun) int {
	type state struct {
		tag   string
		dirty settingSet
	}
	backends := map[int64]*state{}
	n := 0
	for _, s := range r.World.Fake.StatementLog() {
		b := backends[s.Backend]
		if b == nil {
			b = &state{dirty: settingSet{}}
			backends[s.Backend] = b
		}
		if s.Tag != "" && s.Tag != b.tag {
			if len(b.dirty) > 0 {
				n++
			}
			b.tag = s.Tag
		}
		if s.Code != "" || s.Aborted {
			continue
		}
		switch s.Kind {
		case fakepg.KindSet:
			if name, v, ok := parseSet(stripTag(s.Text)); ok {
				b.dirty[name] = v
			}
		case fakepg.KindReset:
			f := strings.Fields(strings.ToLower(stripTag(s.Text)))
			if len(f) == 2 {
				delete(b.dirty, f[1])
			}
		case fakepg.KindDiscard:
			b.dirty = settingSet{}
		}
	}
	return n + observedCrossings(r)
}

func observedCrossings(r *SimRun) int {
	n := 0
	for c, sc := range r.Spec.Clients {
		for i, st := range sc.Steps {
			name, ok := strings.CutPrefix(stripTag(st.SQL), "SHOW ")
			if !ok {
				continue
			}
			got := step(r, c, i)
			for _, s := range sessionSettings {
				if s.name == name && !slices.Contains(got, "D:"+s.def) {
					n++
				}
			}
		}
	}
	return n
}

func checkSession(r *SimRun) error {
	if n := SessionCrossings(r); n != 0 {
		return failf("session state crossed clients %d times", n)
	}
	if err := everyClientFinished(r); err != nil {
		return err
	}
	e := r.Engine
	if e.ResetsCompleted == 0 || e.ResetsCompleted != e.LeasesEndedReset || e.LeasesEndedReset != e.LeasesEnded {
		return failf("resets completed %d, leases ended with a reset %d, leases ended %d", e.ResetsCompleted, e.LeasesEndedReset, e.LeasesEnded)
	}
	if e.ParamStatusTurn == 0 {
		return failf("no ParameterStatus was relayed during a turn")
	}
	return nil
}

func checkVanish(r *SimRun) error {
	for c := 2; c < 4; c++ {
		if err := allSteps(r, c); err != nil {
			return err
		}
		if err := want(r, c, 0, readOK("clean")); err != nil {
			return err
		}
		if err := want(r, c, 1, readOK("init")); err != nil {
			return err
		}
	}
	if n := r.Engine.DBCloses[proxy.CloseDBClientVanished]; n != 3 {
		return failf("%d connections closed for a vanished client, want 3", n)
	}
	lost := 0
	for _, t := range r.World.Fake.TransactionLog() {
		if t.Outcome == fakepg.TxLost {
			lost++
		}
	}
	if lost == 0 {
		return failf("the vanished client's transaction was not lost on the database")
	}
	if r.Engine.DBReady < int64(r.Spec.Conns)+2 {
		return failf("the fixed set did not re-open: %d ready", r.Engine.DBReady)
	}
	return nil
}

func checkKill(row pgwire.ErrRowID) func(r *SimRun) error {
	return func(r *SimRun) error {
		res := r.Clients[0]
		switch row {
		case pgwire.ErrRowLostAfterRead, pgwire.ErrRowLostAfterWrite:
			if err := want(r, 0, 0, errTokens(row.Row().Code)); err != nil {
				return err
			}
			if err := want(r, 0, 1, selectN("5")); err != nil {
				return err
			}
			if r.Engine.ReachedYes != 1 {
				return failf("reached-the-database facts: yes %d no %d", r.Engine.ReachedYes, r.Engine.ReachedNo)
			}
		case pgwire.ErrRowPartialReply:
			got := step(r, 0, 0)
			if HasToken(got, "Z") || !res.Closed || !HasToken(got, "closed:") {
				return failf("a cut reply must end in a closed connection without an end marker: %v", got)
			}
		case pgwire.ErrRowLostInTransaction:
			if err := want(r, 0, 0, []string{"C:BEGIN", "ZT"}); err != nil {
				return err
			}
			if !res.Closed || !slices.Contains(res.Startup, "unsolicited:E:FATAL:08006") {
				return failf("want FATAL 08006 between statements and a closed connection: %s", joinSteps(res))
			}
		}
		if r.Engine.ByRow[row] != 1 {
			return failf("row %s counted %d times", row.Row().ID, r.Engine.ByRow[row])
		}
		if r.Engine.DBReady < 2 {
			return failf("no recovery: %d connections became ready", r.Engine.DBReady)
		}
		return nil
	}
}

func checkKillAfter(r *SimRun) error {
	if err := allSteps(r, 0); err != nil {
		return err
	}
	if err := want(r, 0, 0, readOK("init")); err != nil {
		return err
	}
	e := r.Engine
	if e.ResetsFailed[proxy.ResetFailConnLost]+e.DBCloses[proxy.CloseDBLost] != 1 {
		return failf("the loss after the reply was not seen: reset lost %d, idle lost %d", e.ResetsFailed[proxy.ResetFailConnLost], e.DBCloses[proxy.CloseDBLost])
	}
	return nil
}

func checkSlowReader(r *SimRun) error {
	e := r.Engine
	if e.RelayHighWater > int64(r.Spec.ReplyCap) {
		return failf("relay high-water %d above the cap %d", e.RelayHighWater, r.Spec.ReplyCap)
	}
	if e.RelayPauses == 0 || e.ClientsDropped != 1 || e.ClientCloses[proxy.CloseClientWriteStalled] != 1 {
		return failf("pauses %d dropped %d", e.RelayPauses, e.ClientsDropped)
	}
	if e.DBCloses[proxy.CloseDBClientVanished] != 1 {
		return failf("the slow client's connection was not closed: %v", e.DBCloses)
	}
	for c := 1; c < 3; c++ {
		if err := allSteps(r, c); err != nil {
			return err
		}
	}
	return nil
}

func checkQueueFull(r *SimRun) error {
	shed := 0
	for c := range r.Clients {
		if err := allSteps(r, c); err != nil {
			return err
		}
		if slices.Equal(step(r, c, 0), errTokens("53M01")) {
			shed++
		} else if err := want(r, c, 0, readOK("init")); err != nil {
			return err
		}
		if err := want(r, c, 1, selectN("9")); err != nil {
			return err
		}
	}
	reads := int64(0)
	for _, s := range r.World.Fake.StatementLog() {
		if s.Kind == fakepg.KindRead {
			reads++
		}
	}
	if shed != 3 || r.Engine.ByRow[pgwire.ErrRowShed] != 3 || reads != 3 {
		return failf("shed %d (row %d), database reads %d", shed, r.Engine.ByRow[pgwire.ErrRowShed], reads)
	}
	return nil
}

func within(d, lo, hi time.Duration) bool { return d >= lo && d <= hi }

func checkSilentStartup(r *SimRun) error {
	if err := want(r, 0, 0, errTokens("53M01")); err != nil {
		return err
	}
	if err := want(r, 0, 1, readOK("init")); err != nil {
		return err
	}
	if r.Engine.DBConnectFails[pgwire.ClassDeadline] == 0 {
		return failf("no connect deadline fired: %v", r.Engine.DBConnectFails)
	}
	if el := r.Clients[0].Elapsed[0]; !within(el, shortQueue, shortQueue+50*time.Millisecond) {
		return failf("the queue refusal took %s, budget %s", el, shortQueue)
	}
	return nil
}

func checkSilentQuery(r *SimRun) error {
	if err := want(r, 0, 0, errTokens("08006")); err != nil {
		return err
	}
	if err := want(r, 0, 1, []string{"C:UPDATE 1", "ZI"}); err != nil {
		return err
	}
	if r.Engine.DBCloses[proxy.CloseDBStatementDeadline] != 1 {
		return failf("statement deadline closes %d", r.Engine.DBCloses[proxy.CloseDBStatementDeadline])
	}
	if el := r.Clients[0].Elapsed[0]; !within(el, shortStmt, shortStmt+50*time.Millisecond) {
		return failf("the statement deadline answered after %s, budget %s", el, shortStmt)
	}
	return nil
}

var tamperReason = map[Tamper]proxy.ResetFailure{
	TamperError: proxy.ResetFailError, TamperWrongTag: proxy.ResetFailWrongTag, TamperStatusT: proxy.ResetFailWrongStatus,
	TamperSilent: proxy.ResetFailDeadline, TamperDrop: proxy.ResetFailConnLost, TamperLate: proxy.ResetFailDeadline,
}

func checkTamper(t Tamper) func(r *SimRun) error {
	return func(r *SimRun) error {
		if r.Tamper == nil || r.Tamper.Fired() != 1 {
			return failf("the reset tamper did not fire")
		}
		why := tamperReason[t]
		var failed int64
		for _, v := range r.Engine.ResetsFailed {
			failed += v
		}
		if r.Engine.ResetsFailed[why] != 1 || failed != 1 || r.Engine.DBCloses[proxy.CloseDBResetFailed] != 1 {
			return failf("reset failures %v, want one %s", r.Engine.ResetsFailed, why)
		}
		if err := everyClientFinished(r); err != nil {
			return err
		}
		after := false
		for _, s := range r.World.Fake.StatementLog() {
			if s.Backend != r.Tamper.backend {
				continue
			}
			if after {
				return failf("backend %d ran %q after its failed reset", s.Backend, s.Text)
			}
			after = s.Kind == fakepg.KindDiscard
		}
		return nil
	}
}
