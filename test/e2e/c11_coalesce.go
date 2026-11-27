package e2e

import (
	"fmt"
	"os"
	"slices"
	"strings"
	"time"

	"github.com/nickstrad/mini-gateway/internal/coalesce"
	"github.com/nickstrad/mini-gateway/internal/pool"
	"github.com/nickstrad/mini-gateway/internal/sim"
	"github.com/nickstrad/mini-gateway/internal/sim/fakepg"
	"github.com/nickstrad/mini-gateway/internal/work"
)

const (
	hotRow       = "hot"
	hotValue     = "v1"
	hotService   = 50 * time.Millisecond
	hotClients   = 1000
	hotSpread    = 3 * time.Microsecond
	cancelGroup  = 100
	cancelAfter  = 100 * time.Millisecond
	slowService  = 300 * time.Millisecond
	bigValueLen  = 40 << 10
	hugeValueLen = 100 << 10
	recoveryGap  = time.Second
)

type CoalesceScenario struct {
	Spec    Spec
	Heavy   bool
	Check   func(r *SimRun) error
	Ablate  func(Spec) Spec
	Direct  bool
	Ablated string
}

func hotRead() string { return "SELECT v FROM kv WHERE k = '" + hotRow + "'" }

func hotReadOf(k string) string { return "SELECT v FROM kv WHERE k = '" + k + "'" }

func coalesceAuto(join coalesce.JoinMode) coalesce.Config {
	return coalesce.Config{Mode: coalesce.ModeAuto, Join: join}
}

func joinName(j coalesce.JoinMode) string { return j.String() }

func coalRows(extra ...fakepg.Row) []fakepg.Row {
	return append(clientRows(4), append([]fakepg.Row{{Key: hotRow, Value: hotValue}, {Key: "hot2", Value: hotValue}}, extra...)...)
}

func readService(d time.Duration) fakepg.Service {
	return fakepg.Service{PerKind: []fakepg.KindTime{{Kind: fakepg.KindRead, Time: d}}}
}

func coalBase(name string, join coalesce.JoinMode, service time.Duration) Spec {
	sp := Spec{Name: name, Tenants: 4, Conns: 4, QueueCap: 1024, Rows: coalRows(), Timeouts: poolTimeouts(),
		Duration: 10 * time.Second, Service: readService(service), Coalesce: coalesceAuto(join)}
	sp.Timeouts.QueueWait = 20 * time.Second
	return withCore(sp, name, func(c *pool.Config) { c.RatePerSec = 1000; c.MaxConnecting = 4 })
}

func coalesceOff(sp Spec) Spec {
	sp.Coalesce = coalesce.Config{}
	return sp
}

func withPlant(p coalesce.Plant) func(Spec) Spec {
	return func(sp Spec) Spec {
		sp.Coalesce.Plant |= p
		return sp
	}
}

func HotKeySpec(join coalesce.JoinMode) Spec {
	sp := coalBase("coalesce-hot-key-"+joinName(join), join, hotService)
	sp.Duration = 20 * time.Second
	for i := 0; i < hotClients; i++ {
		sp.Clients = append(sp.Clients, Script{Tenant: i % 4, Start: time.Duration(i) * hotSpread,
			Steps: []Step{{SQL: hotRead()}, {SQL: "SELECT 1", Delay: recoveryGap}}})
	}
	sp.FakeConns = hotClients + 64
	return sp
}

func readsOf(r *SimRun, text string) []fakepg.StatementRecord {
	var out []fakepg.StatementRecord
	for _, s := range r.World.Fake.StatementLog() {
		if s.Kind == fakepg.KindRead && s.Text == text {
			out = append(out, s)
		}
	}
	return out
}

func MaxConcurrent(recs []fakepg.StatementRecord) int {
	type edge struct {
		at    sim.Time
		delta int
	}
	var edges []edge
	for _, s := range recs {
		edges = append(edges, edge{s.Start, 1}, edge{s.End, -1})
	}
	slices.SortFunc(edges, func(a, b edge) int {
		if a.at != b.at {
			if a.at < b.at {
				return -1
			}
			return 1
		}
		return a.delta - b.delta
	})
	most, cur := 0, 0
	for _, e := range edges {
		cur += e.delta
		most = max(most, cur)
	}
	return most
}

func stepOf(r *SimRun, c, i int) []string {
	if i < len(r.Clients[c].Steps) {
		return r.Clients[c].Steps[i]
	}
	return nil
}

func gotValue(st []string, v string) bool {
	return len(st) > 0 && st[len(st)-1] == "ZI" && slices.Contains(st, "D:"+v) && !HasToken(st, "E:")
}

func Percentile(d []time.Duration, permille int) time.Duration {
	if len(d) == 0 {
		return 0
	}
	s := slices.Clone(d)
	slices.Sort(s)
	i := (len(s)*permille + 999) / 1000
	return s[min(max(i-1, 0), len(s)-1)]
}

func firstStepTimes(r *SimRun) []time.Duration {
	var out []time.Duration
	for _, c := range r.Clients {
		if len(c.Elapsed) > 0 {
			out = append(out, c.Elapsed[0])
		}
	}
	return out
}

func coalSnapOK(r *SimRun) error {
	s := r.World.Proxy.Engine.CoalesceSnapshot()
	if err := s.Check(); err != nil {
		return err
	}
	if s.Offered != s.Started+s.Hits+s.Refused+s.PassedNoShare+s.PassedFull+s.PassedDuplicate {
		return failf("coalescer conservation broken: %+v", s)
	}
	if s.Flights.Value != 0 || s.Waiting.Value != 0 || s.Entries.Value != 0 || s.StoredBytes.Value != 0 {
		return failf("coalescer not empty after the run: %d flights, %d waiting", s.Flights.Value, s.Waiting.Value)
	}
	if r.Engine.Coalesced.Value != 0 {
		return failf("%d requests still waiting on flights", r.Engine.Coalesced.Value)
	}
	return nil
}

func recovered(r *SimRun, step int) error {
	for c := range r.Clients {
		st := stepOf(r, c, step)
		if !gotValue(st, "1") {
			return failf("client %d did not recover: step %d %v", c, step, st)
		}
	}
	return nil
}

func checkHotKey(r *SimRun) error {
	if err := everyClientFinished(r); err != nil {
		return err
	}
	reads := readsOf(r, hotRead())
	if len(reads) < 1 || len(reads) > 2 {
		return failf("%d database reads for a burst of %d identical reads, want 1 or 2", len(reads), len(r.Clients))
	}
	if n := MaxConcurrent(reads); n > 1 {
		return failf("%d reads of one key running on the database at once", n)
	}
	for c := range r.Clients {
		if st := stepOf(r, c, 0); !gotValue(st, hotValue) {
			return failf("client %d: %v", c, st)
		}
	}
	limit := 3 * hotService / 2
	if r.Spec.Coalesce.Join == coalesce.JoinStrict {
		limit = 5 * hotService / 2
	}
	if p99 := Percentile(firstStepTimes(r), 990); p99 > limit {
		return failf("p99 %s above %s", p99, limit)
	}
	s := r.World.Proxy.Engine.CoalesceSnapshot()
	if s.Hits < int64(len(r.Clients)-2) || r.Engine.CoalesceReplies < int64(len(r.Clients)-2) {
		return failf("hits %d, shared replies %d for %d clients", s.Hits, r.Engine.CoalesceReplies, len(r.Clients))
	}
	if err := recovered(r, 1); err != nil {
		return err
	}
	return coalSnapOK(r)
}

func WritesSpec(join coalesce.JoinMode, hinted bool) Spec {
	name := "coalesce-writes-" + joinName(join)
	sp := coalBase(name, join, 5*time.Millisecond)
	sp.Duration = 5 * time.Second
	sp.Service.PerKind = append(sp.Service.PerKind, fakepg.KindTime{Kind: fakepg.KindUpdate, Time: 2 * time.Millisecond})
	mark := ""
	if hinted {
		sp.Name += "-hinted"
		sp.Coalesce.Mode = coalesce.ModeHinted
		mark = "/* mg:share */ "
	}
	for c := 0; c < 40; c++ {
		own := c % 4
		w := func(v string) string { return mark + "UPDATE kv SET v = '" + v + "' WHERE k = '" + hotRow + "'" }
		sp.Clients = append(sp.Clients, Script{Tenant: own, Start: time.Duration(c%5) * time.Millisecond, Steps: []Step{
			{SQL: mark + hotRead()},
			{SQL: w("w" + itoa(c) + "a"), Delay: time.Millisecond},
			{SQL: mark + hotRead(), Delay: time.Millisecond},
			{SQL: "BEGIN", Delay: time.Millisecond},
			{SQL: hotRead(), Delay: time.Millisecond},
			{SQL: w("w" + itoa(c) + "b"), Delay: time.Millisecond},
			{SQL: hotRead(), Delay: time.Millisecond},
			{SQL: "COMMIT", Delay: time.Millisecond},
			{SQL: mark + hotRead(), Delay: time.Millisecond},
		}})
	}
	return sp
}

func MarkedWriteSpec() Spec {
	sp := coalBase("coalesce-marked-write-hinted", coalesce.JoinStrict, hotService)
	sp.Coalesce.Mode = coalesce.ModeHinted
	sp.Service.PerKind = append(sp.Service.PerKind, fakepg.KindTime{Kind: fakepg.KindUpdate, Time: hotService})
	for i := 0; i < 20; i++ {
		sp.Clients = append(sp.Clients, Script{Tenant: i % 4, Start: time.Duration(i) * 10 * time.Microsecond,
			Steps: []Step{{SQL: "/* mg:share */ UPDATE kv SET v = 'same' WHERE k = '" + hotRow + "'"}}})
	}
	return sp
}

func checkWriteCount(r *SimRun) error {
	sent := 0
	for _, sc := range r.Spec.Clients {
		for _, st := range sc.Steps {
			if strings.Contains(st.SQL, "UPDATE") {
				sent++
			}
		}
	}
	if n := len(r.World.Fake.WriteLog()); n != sent {
		return failf("%d writes reached the database for %d sent", n, sent)
	}
	return nil
}

func checkWrites(r *SimRun) error {
	if err := everyClientFinished(r); err != nil {
		return err
	}
	writes := map[string]int{}
	for _, w := range r.World.Fake.WriteLog() {
		writes[w.Value]++
	}
	inTx := map[int64]bool{}
	inTxReads := 0
	for _, st := range r.World.Fake.StatementLog() {
		switch st.Kind {
		case fakepg.KindBegin:
			inTx[st.Backend] = true
		case fakepg.KindCommit, fakepg.KindRollback:
			inTx[st.Backend] = false
		case fakepg.KindRead:
			if inTx[st.Backend] && st.Text == hotRead() {
				inTxReads++
			}
		}
	}
	sent := 0
	for c := range r.Spec.Clients {
		for _, v := range []string{"w" + itoa(c) + "a", "w" + itoa(c) + "b"} {
			sent++
			if writes[v] != 1 {
				return failf("write %s appears %d times in the database's write log, want exactly once", v, writes[v])
			}
		}
		for i, st := range r.Clients[c].Steps {
			if HasToken(st, "E:") {
				return failf("client %d step %d failed: %v", c, i, st)
			}
		}
	}
	if len(r.World.Fake.WriteLog()) != sent {
		return failf("%d writes in the log for %d sent", len(r.World.Fake.WriteLog()), sent)
	}
	if want := 2 * len(r.Spec.Clients); inTxReads != want {
		return failf("%d reads inside transactions reached the database, %d were sent: one was shared", inTxReads, want)
	}
	s := r.World.Proxy.Engine.CoalesceSnapshot()
	if s.Offered == 0 || s.NotEligible == 0 {
		return failf("counters did not move: offered %d, not eligible %d", s.Offered, s.NotEligible)
	}
	return coalSnapOK(r)
}

func CoalCancelSpec(join coalesce.JoinMode, all bool) Spec {
	name := "coalesce-cancel-99-" + joinName(join)
	if all {
		name = "coalesce-cancel-all-" + joinName(join)
	}
	sp := coalBase(name, join, slowService)
	for i := 0; i < cancelGroup; i++ {
		st := Step{SQL: hotRead()}
		if all || i < cancelGroup-1 {
			st.Cancel = cancelAfter
		}
		sp.Clients = append(sp.Clients, Script{Tenant: i % 4, Start: time.Duration(i) * 10 * time.Microsecond,
			Steps: []Step{st, {SQL: "SELECT 1", Delay: recoveryGap}}})
	}
	sp.FakeConns = 256
	return sp
}

func cancelled(st []string) bool {
	return HasToken(st, "E:ERROR:57014") && len(st) > 0 && st[len(st)-1] == "ZI"
}

func checkCancel99(r *SimRun) error {
	if err := everyClientFinished(r); err != nil {
		return err
	}
	last := len(r.Clients) - 1
	if st := stepOf(r, last, 0); !gotValue(st, hotValue) {
		return failf("the waiter that stayed did not get a valid reply: %v", st)
	}
	for c := 0; c < last; c++ {
		if st := stepOf(r, c, 0); !cancelled(st) {
			return failf("client %d cancelled and got %v, want its own 57014", c, st)
		}
	}
	reads := readsOf(r, hotRead())
	aborted := 0
	for _, s := range reads {
		if s.Code != "" || s.Aborted {
			aborted++
		}
	}
	if len(reads) < 1 || len(reads) > 2 || aborted == len(reads) {
		return failf("%d database reads, %d cancelled on the database", len(reads), aborted)
	}
	if aborted > 0 && r.Spec.Coalesce.Join != coalesce.JoinStrict {
		return failf("relaxed: every waiter joined one read, yet a read was cancelled on the database")
	}
	if got := r.Engine.CancelsCoalesced + r.Engine.CancelsForwarded; got != int64(last) {
		return failf("%d waiters answered their own cancel and %d cancels forwarded, want %d together", r.Engine.CancelsCoalesced,
			r.Engine.CancelsForwarded, last)
	}
	if r.Engine.CoalesceHandoffs == 0 {
		return failf("the first waiter cancelled but no handoff happened")
	}
	if err := recovered(r, 1); err != nil {
		return err
	}
	return coalSnapOK(r)
}

func checkCancelAll(r *SimRun) error {
	if err := everyClientFinished(r); err != nil {
		return err
	}
	for c := range r.Clients {
		if st := stepOf(r, c, 0); !cancelled(st) {
			return failf("client %d: %v", c, st)
		}
	}
	reads := readsOf(r, hotRead())
	aborted := 0
	for _, s := range reads {
		if s.Code == "57014" {
			aborted++
		}
	}
	if len(reads) < 1 || len(reads) > 2 || aborted != len(reads) {
		return failf("%d reads, %d cancelled on the database, want every read cancelled", len(reads), aborted)
	}
	if err := recovered(r, 1); err != nil {
		return err
	}
	return coalSnapOK(r)
}

func VanishSpec(join coalesce.JoinMode) Spec {
	sp := coalBase("coalesce-vanish-all-"+joinName(join), join, slowService)
	for i := 0; i < cancelGroup; i++ {
		sp.Clients = append(sp.Clients, Script{Tenant: i % 4, Start: time.Duration(i) * 10 * time.Microsecond,
			Steps: []Step{{SQL: hotRead(), Vanish: VanishAfterSend}}})
	}
	sp.Clients = append(sp.Clients, Script{Tenant: 0, Start: 2 * time.Second, Steps: []Step{{SQL: hotRead()}, {SQL: "SELECT 1", Delay: 10 * time.Millisecond}}})
	sp.FakeConns = 256
	return sp
}

func checkCoalVanish(r *SimRun) error {
	var burst []fakepg.StatementRecord
	for _, s := range readsOf(r, hotRead()) {
		if s.Start < sim.Time(time.Second) {
			burst = append(burst, s)
		}
	}
	if len(burst) < 1 || len(burst) > 2 {
		return failf("%d database reads for the burst", len(burst))
	}
	for _, s := range burst {
		if s.Code != "57014" {
			return failf("a read nobody waited for was not cancelled on the database (code %q)", s.Code)
		}
	}
	ps := poolSnap(r)
	if ps.Ends[pool.EndVanishedMidStatement] == 0 || ps.Cancels == 0 {
		return failf("the connection did not go through the dirty table: %d vanished mid-statement, %d cancels",
			ps.Ends[pool.EndVanishedMidStatement], ps.Cancels)
	}
	last := len(r.Clients) - 1
	if !gotValue(stepOf(r, last, 0), hotValue) || !gotValue(stepOf(r, last, 1), "1") {
		return failf("no recovery: %v", r.Clients[last])
	}
	return coalSnapOK(r)
}

func RYWSpec(join coalesce.JoinMode) Spec {
	sp := coalBase("coalesce-read-your-write-"+joinName(join), join, 100*time.Millisecond)
	sp.Service.PerKind = append(sp.Service.PerKind, fakepg.KindTime{Kind: fakepg.KindUpdate, Time: time.Millisecond})
	sp.Duration = 3 * time.Second
	sp.Clients = []Script{
		{Tenant: 0, Steps: []Step{{SQL: hotRead()}}},
		{Tenant: 1, Start: 10 * time.Millisecond, Steps: []Step{
			{SQL: "UPDATE kv SET v = 'new' WHERE k = '" + hotRow + "'"},
			{SQL: hotRead()},
		}},
	}
	return sp
}

func checkRYW(r *SimRun) error {
	if err := everyClientFinished(r); err != nil {
		return err
	}
	if !gotValue(stepOf(r, 0, 0), hotValue) {
		return failf("the first reader: %v", r.Clients[0])
	}
	after := stepOf(r, 1, 1)
	switch r.Spec.Coalesce.Join {
	case coalesce.JoinStrict:
		if !gotValue(after, "new") {
			return failf("strict: a read after the client's own acknowledged write returned %v", after)
		}
	default:
		if !gotValue(after, hotValue) {
			return failf("relaxed: the read after the write should join the read already running and show the window, got %v", after)
		}
	}
	return coalSnapOK(r)
}

func SlowFollowerSpec(join coalesce.JoinMode) Spec {
	sp := coalBase("coalesce-slow-follower-"+joinName(join), join, hotService)
	sp.Rows = coalRows(fakepg.Row{Key: "big", Value: strings.Repeat("x", bigValueLen)})
	sp.Duration = 5 * time.Second
	for i := 0; i < 20; i++ {
		st := Step{SQL: hotReadOf("big")}
		if i < 3 {
			st.StopReading, st.StallFor = true, 2*time.Second
		}
		sp.Clients = append(sp.Clients, Script{Tenant: i % 4, Start: time.Duration(i) * 10 * time.Microsecond, Steps: []Step{st}})
	}
	return sp
}

func checkSlowFollower(r *SimRun) error {
	if err := everyClientFinished(r); err != nil {
		return err
	}
	want := "D:" + strings.Repeat("x", bigValueLen)
	for c := range r.Clients {
		st := stepOf(r, c, 0)
		if !slices.Contains(st, want) || st[len(st)-1] != "ZI" {
			return failf("client %d did not get the complete reply", c)
		}
		el := r.Clients[c].Elapsed[0]
		if c >= 3 && el > 3*hotService {
			return failf("client %d waited %s behind slow followers", c, el)
		}
		if c < 3 && el < 2*time.Second {
			return failf("slow client %d finished after %s, before it read again", c, el)
		}
	}
	if s := r.World.Proxy.Engine.CoalesceSnapshot(); s.Hits == 0 {
		return failf("no follower")
	}
	return coalSnapOK(r)
}

func SharedErrorSpec(join coalesce.JoinMode) Spec {
	sp := coalBase("coalesce-shared-error-"+joinName(join), join, 2*time.Second)
	sp.Duration = 8 * time.Second
	for i := 0; i < cancelGroup; i++ {
		sp.Clients = append(sp.Clients, Script{Tenant: i % 4, Start: time.Duration(i) * 10 * time.Microsecond,
			Steps: []Step{{SQL: hotRead()}, {SQL: "SELECT 1", Delay: 3 * time.Second}}})
	}
	sp.FakeConns = 256
	return sp
}

func checkSharedError(r *SimRun) error {
	if err := everyClientFinished(r); err != nil {
		return err
	}
	lost := 0
	for c := range r.Clients {
		st := stepOf(r, c, 0)
		n := 0
		for _, tk := range st {
			if strings.HasPrefix(tk, "E:") {
				n++
			}
		}
		switch {
		case n == 1 && HasToken(st, "E:ERROR:08006"):
			lost++
		case n == 1 && HasToken(st, "E:ERROR:53M01"):
		default:
			return failf("client %d: %v, want one shared error (08006 from the read that ran, 53M01 from a flight that expired before it was sent)", c, st)
		}
	}
	if lost == 0 {
		return failf("no client got the running flight's 08006")
	}
	if reads := readsOf(r, hotRead()); len(reads) > 2 || len(reads) == 0 {
		return failf("%d database reads for the burst: the failure was re-driven", len(reads))
	}
	if r.World.Proxy.Engine.CoalesceSnapshot().SharedErrors == 0 {
		return failf("no shared error counted")
	}
	if err := recovered(r, 1); err != nil {
		return err
	}
	return coalSnapOK(r)
}

func CapsSpec(join coalesce.JoinMode) Spec {
	sp := coalBase("coalesce-caps-"+joinName(join), join, hotService)
	sp.Rows = coalRows(fakepg.Row{Key: "huge", Value: strings.Repeat("y", hugeValueLen)})
	sp.Coalesce.MaxWaiters = 10
	sp.Coalesce.NoShareFor = coalesce.Instant(time.Second)
	sp.Duration = 6 * time.Second
	for i := 0; i < 30; i++ {
		sp.Clients = append(sp.Clients, Script{Tenant: i % 4, Start: time.Duration(i) * 10 * time.Microsecond, Steps: []Step{{SQL: hotRead()}}})
	}
	for i := 0; i < 10; i++ {
		sp.Clients = append(sp.Clients, Script{Tenant: i % 4, Start: 500*time.Millisecond + time.Duration(i)*10*time.Microsecond,
			Steps: []Step{{SQL: hotReadOf("huge")}, {SQL: hotReadOf("huge"), Delay: 100 * time.Millisecond}, {SQL: hotReadOf("huge"), Delay: 2 * time.Second}}})
	}
	return sp
}

func checkCaps(r *SimRun) error {
	if err := everyClientFinished(r); err != nil {
		return err
	}
	refused, served := 0, 0
	for c := 0; c < 30; c++ {
		st := stepOf(r, c, 0)
		switch {
		case HasToken(st, "E:ERROR:53M04"):
			refused++
		case gotValue(st, hotValue):
			served++
		default:
			return failf("client %d: %v", c, st)
		}
	}
	if refused == 0 || served == 0 {
		return failf("waiter cap: %d refused 53M04, %d served", refused, served)
	}
	huge := "D:" + strings.Repeat("y", hugeValueLen)
	full, capped := 0, 0
	for c := 30; c < len(r.Clients); c++ {
		st := stepOf(r, c, 0)
		switch {
		case HasToken(st, "E:ERROR:53M04"):
			capped++
		case slices.Contains(st, huge):
			full++
		default:
			return failf("client %d: first huge read %v", c, st[:min(len(st), 3)])
		}
		if st := stepOf(r, c, 1); !slices.Contains(st, huge) {
			return failf("client %d step 1: a key on the no-share list must be served unshared", c)
		}
		if st := stepOf(r, c, 2); !slices.Contains(st, huge) && !HasToken(st, "E:ERROR:53M04") {
			return failf("client %d step 2: %v", c, st[:min(len(st), 3)])
		}
	}
	if full == 0 || capped == 0 {
		return failf("reply cap: %d full replies, %d refused", full, capped)
	}
	s := r.World.Proxy.Engine.CoalesceSnapshot()
	if s.Overflows[coalesce.CapWaiters] == 0 || s.Overflows[coalesce.CapReply] == 0 || s.PassedNoShare == 0 || s.NoShareExpired == 0 {
		return failf("caps did not move: %+v no-share passed %d expired %d", s.Overflows, s.PassedNoShare, s.NoShareExpired)
	}
	return coalSnapOK(r)
}

func HintedSpec() Spec {
	sp := coalBase("coalesce-hinted", coalesce.JoinStrict, hotService)
	sp.Coalesce.Mode = coalesce.ModeHinted
	for i := 0; i < 50; i++ {
		sp.Clients = append(sp.Clients, Script{Tenant: i % 4, Start: time.Duration(i) * 10 * time.Microsecond, Steps: []Step{
			{SQL: "/* mg:share */ " + hotRead()}, {SQL: hotReadOf("hot2"), Delay: 200 * time.Millisecond}}})
	}
	return sp
}

func checkHinted(r *SimRun) error {
	if err := everyClientFinished(r); err != nil {
		return err
	}
	marked := readsOf(r, "/* mg:share */ "+hotRead())
	if len(marked) == 0 {
		marked = readsOf(r, hotRead())
	}
	plain := readsOf(r, hotReadOf("hot2"))
	if len(marked) < 1 || len(marked) > 2 {
		return failf("%d reads of the marked statement", len(marked))
	}
	if len(plain) != len(r.Clients) {
		return failf("%d reads of the unmarked safe-shape statement under hinted, want %d (one each)", len(plain), len(r.Clients))
	}
	for c := range r.Clients {
		if !gotValue(stepOf(r, c, 0), hotValue) || !gotValue(stepOf(r, c, 1), hotValue) {
			return failf("client %d: %v", c, r.Clients[c])
		}
	}
	return coalSnapOK(r)
}

func CoalesceResultsSpec(join coalesce.JoinMode) Spec {
	sp := ResultsSpec("coalesce-results-"+joinName(join), false)
	sp.Rows = append(sp.Rows, fakepg.Row{Key: hotRow, Value: hotValue})
	sp.Service = readService(5 * time.Millisecond)
	sp.Coalesce = coalesceAuto(join)
	for c := range sp.Clients {
		sp.Clients[c].Steps = append([]Step{{SQL: hotRead(), Delay: stepGap}}, sp.Clients[c].Steps...)
	}
	return sp
}

func checkCoalesceResults(r *SimRun) error {
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
		if err := want(r, c, 13, []string{"T", "D:7", "C:SELECT 1", "ZI"}); err != nil {
			return err
		}
	}
	if r.World.Proxy.Engine.CoalesceSnapshot().Hits == 0 {
		return failf("no request was shared: the scenario does not exercise the coalescer")
	}
	return coalSnapOK(r)
}

func TenantScopeSpec() Spec {
	sp := coalBase("coalesce-tenant-scope", coalesce.JoinStrict, hotService)
	sp.KeyScope = work.ScopeTenant
	for i := 0; i < 40; i++ {
		sp.Clients = append(sp.Clients, Script{Tenant: i % 4, Start: time.Duration(i) * 10 * time.Microsecond, Steps: []Step{{SQL: hotRead()}}})
	}
	return sp
}

func checkTenantScope(r *SimRun) error {
	if err := everyClientFinished(r); err != nil {
		return err
	}
	reads := readsOf(r, hotRead())
	if len(reads) < 4 || len(reads) > 8 {
		return failf("%d reads for 4 tenants with a per-tenant key, want 4 to 8", len(reads))
	}
	s := r.World.Proxy.Engine.CoalesceSnapshot()
	if len(s.PerTenant) != 4 {
		return failf("hits counted for %d tenants, want 4", len(s.PerTenant))
	}
	return coalSnapOK(r)
}

func CoalesceScenarios() []CoalesceScenario {
	var out []CoalesceScenario
	for _, j := range []coalesce.JoinMode{coalesce.JoinStrict, coalesce.JoinRelaxed} {
		out = append(out,
			CoalesceScenario{Spec: HotKeySpec(j), Heavy: true, Check: checkHotKey, Ablate: coalesceOff, Direct: true, Ablated: "coalescer off"},
			CoalesceScenario{Spec: WritesSpec(j, false), Check: checkWrites},
			CoalesceScenario{Spec: CoalCancelSpec(j, false), Check: checkCancel99, Ablate: withPlant(coalesce.PlantAnyLeaveCancels), Ablated: "any leave cancels"},
			CoalesceScenario{Spec: CoalCancelSpec(j, true), Check: checkCancelAll, Ablate: coalesceOff, Ablated: "coalescer off"},
			CoalesceScenario{Spec: VanishSpec(j), Check: checkCoalVanish, Ablate: coalesceOff, Ablated: "coalescer off"},
			CoalesceScenario{Spec: RYWSpec(j), Check: checkRYW},
			CoalesceScenario{Spec: SlowFollowerSpec(j), Check: checkSlowFollower},
			CoalesceScenario{Spec: SharedErrorSpec(j), Check: checkSharedError, Ablate: coalesceOff, Ablated: "coalescer off"},
			CoalesceScenario{Spec: CapsSpec(j), Check: checkCaps, Ablate: withPlant(coalesce.PlantNoWaiterCap | coalesce.PlantNoReplyCap), Ablated: "no caps"},
			CoalesceScenario{Spec: CoalesceResultsSpec(j), Check: checkCoalesceResults},
		)
	}
	out[5].Ablate, out[5].Ablated = withPlant(coalesce.PlantStrictJoinsRunning), "strict joins the running read"
	out = append(out,
		CoalesceScenario{Spec: MarkedWriteSpec(), Check: func(r *SimRun) error {
			if err := checkWriteCount(r); err == nil {
				return failf("identical marked writes were never shared under hinted: the write check cannot fail")
			}
			return coalSnapOK(r)
		}},
		CoalesceScenario{Spec: HintedSpec(), Check: checkHinted, Ablate: func(sp Spec) Spec { sp.Coalesce.Mode = coalesce.ModeAuto; return sp }, Ablated: "auto instead of hinted"},
		CoalesceScenario{Spec: TenantScopeSpec(), Check: checkTenantScope},
	)
	return out
}

func (sc CoalesceScenario) String() string { return fmt.Sprintf("%s (%s)", sc.Spec.Name, sc.Ablated) }

func coalFingerprint(sp Spec) string {
	if sp.Coalesce.Mode == coalesce.ModeOff {
		return ""
	}
	return fmt.Sprintf("-co%s-%s-s%d-w%d-x%d", sp.Coalesce.Mode, sp.Coalesce.Join, sp.KeyScope, sp.Coalesce.MaxWaiters, sp.Coalesce.Plant)
}

func (sc CoalesceScenario) AblationArm() *Spec {
	if sc.Ablate == nil {
		return nil
	}
	ab := sc.Ablate(sc.Spec)
	if ab.Coalesce.Mode != coalesce.ModeOff {
		return nil
	}
	return &ab
}

const CoalesceHeavyEnv = "MG_C11_HEAVY"

func CoalesceHeavyRuns(short bool) bool {
	return !short && (os.Getenv("RUN_DIR") != "" || os.Getenv(CoalesceHeavyEnv) == "1")
}
