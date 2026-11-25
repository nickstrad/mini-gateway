package fakepg

import (
	"strconv"
	"strings"
	"time"

	"github.com/nickstrad/mini-gateway/internal/sim"
)

type pendingWrite struct {
	op      Kind
	key     string
	value   string
	text    string
	tag     string
	stmtSeq int64
}

type txn struct {
	id       int64
	explicit bool
	failed   bool
	writes   []pendingWrite
	saved    []setting
}

const MaxTxWrites = 1 << 16

func (srv *Server) newTx(explicit bool) *txn {
	srv.txSeq++
	srv.stats.txBegun++
	srv.stats.txOpen++
	return &txn{id: srv.txSeq, explicit: explicit}
}

func (b *backend) beginMessage(q string) {
	texts := Split(q)
	b.firstReply = true
	if len(texts) > MaxStatements {
		b.emit(encError(nil, errorf("ERROR", "54000", "fakepg: more than %d statements in one query", MaxStatements)))
		b.endMessage()
		return
	}
	if len(texts) == 0 {
		b.emit(encEmptyQuery(nil))
		b.endMessage()
		return
	}
	b.msg = make([]Stmt, 0, len(texts))
	for _, t := range texts {
		b.msg = append(b.msg, Match(t))
	}
	b.msgIdx = 0
	b.msgImplicit = len(b.msg) > 1
	if b.muteQueries || b.srv.faults.silentQuery {
		r := b.newRun(b.msg[0])
		r.state = runMuted
		b.srv.stats.inService.Add(1)
		b.cur = r
		b.msg = nil
		b.srv.s.Trace(kindFire, b.id, int64(FaultSilentQuery), 0, 0)
	}
}

func (b *backend) newRun(st Stmt) *run {
	srv := b.srv
	srv.stmtSeq++
	srv.stats.started++
	srv.stats.byKind[st.Kind]++
	r := &run{st: st, seq: srv.stmtSeq, start: srv.s.Now()}
	if b.tx != nil {
		r.txID = b.tx.id
	}
	srv.s.Trace(kindStmt, b.id, int64(st.Kind), r.txID, r.seq)
	return r
}

func (b *backend) startNext() {
	if b.msgIdx >= len(b.msg) {
		b.msg = nil
		b.endMessage()
		return
	}
	st := b.msg[b.msgIdx]
	b.msgIdx++
	if b.msgImplicit && b.tx == nil {
		b.tx = b.srv.newTx(false)
	}
	r := b.newRun(st)
	b.cur = r
	if b.tx != nil && b.tx.failed && st.Kind != KindCommit && st.Kind != KindRollback {
		r.state = runDone
		return
	}
	if b.armed(pointBefore) {
		b.after = afterReset
		r.state = runHeld
		b.srv.stats.inService.Add(1)
		return
	}
	if !b.srv.slotFree() {
		r.state = runWaiting
		b.srv.stats.waiting.Add(1)
		b.srv.waitQ = append(b.srv.waitQ, b)
		return
	}
	b.beginService(r)
}

func (srv *Server) slotFree() bool {
	return srv.cfg.Capacity <= 0 || srv.slots < srv.cfg.Capacity
}

func (b *backend) beginService(r *run) {
	srv := b.srv
	r.state = runServing
	r.slot = true
	srv.slots++
	srv.stats.inService.Add(1)
	if r.st.Kind == KindRead {
		r.readVal, r.readFound = b.read(r.st.Key)
	}
	d := srv.serviceTime(b, r)
	if srv.faults.frozen {
		r.remaining = d
	} else {
		r.timer = srv.s.After(d, func() { b.serviceDone(r) })
	}
	if b.describeAtStart(r) && !srv.faults.frozen {
		b.pump()
	}
}

func (b *backend) describeAtStart(r *run) bool {
	st := r.st
	switch st.Kind {
	case KindConst:
		oid, size := oidText, int16(-1)
		if _, err := strconv.ParseInt(st.Const, 10, 32); err == nil {
			oid, size = oidInt4, 4
		}
		b.emit(encRowDesc(nil, "?column?", oid, size))
	case KindTxid:
		b.emit(encRowDesc(nil, "?column?", oidBool, 1))
	case KindRead:
		if st.Const == "sleep" {
			b.emit(encRowDesc2(nil, "v", oidText, "?column?", oidBool))
		} else {
			b.emit(encRowDesc(nil, "v", oidText, -1))
		}
	case KindSleep:
		b.emit(encRowDesc(nil, "pg_sleep", oidVoid, 4))
	case KindRows:
		b.emit(encRowDesc(nil, "repeat", oidText, -1))
	case KindShow:
		if findSetting(b.settings, st.Name) < 0 {
			return false
		}
		b.emit(encRowDesc(nil, strings.ToLower(st.Name), oidText, -1))
	default:
		return false
	}
	r.described = true
	return true
}

func (b *backend) serviceDone(r *run) {
	if b.cur != r || r.state != runServing {
		return
	}
	b.leaveService(r)
	b.advance()
}

func (b *backend) leaveService(r *run) {
	srv := b.srv
	switch r.state {
	case runWaiting:
		srv.stats.waiting.Add(-1)
		for i, w := range srv.waitQ {
			if w == b {
				srv.waitQ = append(srv.waitQ[:i], srv.waitQ[i+1:]...)
				break
			}
		}
	case runServing, runMuted, runHeld:
		srv.stats.inService.Add(-1)
		r.timer.Cancel()
	}
	r.state = runDone
	if r.slot {
		r.slot = false
		srv.slots--
		srv.admitWaiter()
	}
}

func (srv *Server) admitWaiter() {
	for len(srv.waitQ) > 0 && srv.slotFree() && !srv.faults.frozen {
		b := srv.waitQ[0]
		srv.waitQ = srv.waitQ[1:]
		srv.stats.waiting.Add(-1)
		b.beginService(b.cur)
	}
}

func (srv *Server) serviceTime(b *backend, r *run) time.Duration {
	if srv.faults.boundaryOn {
		return srv.faults.boundary
	}
	sv := &srv.cfg.Service
	d := sv.Fixed
	for _, kt := range sv.PerKind {
		if kt.Kind == r.st.Kind {
			d = kt.Time
		}
	}
	if sv.JitterMax > 0 {
		d += time.Duration(b.stream.Int64N(int64(sv.JitterMax) + 1))
	}
	if r.st.Key != "" {
		for _, kt := range sv.PerKey {
			if kt.Key == r.st.Key {
				d = sim.AddDurations(d, kt.Time)
			}
		}
	}
	d = sim.AddDurations(d, r.st.Sleep)
	d = sim.AddDurations(d, b.slowExtra)
	start := srv.s.Now()
	for _, w := range srv.faults.stalls {
		end := sim.Later(start, d)
		if start < w.To && end > w.From {
			d = sim.AddDurations(d, w.Extra)
		}
	}
	return d
}

func (b *backend) finishStatement() {
	r := b.cur
	b.cur = nil
	srv := b.srv
	if b.garbled() {
		srv.stats.finished++
		srv.logStatement(b, r, "", false)
		return
	}
	cut := b.armed(pointDuring)
	before := len(b.out)
	e := b.execute(r)
	if cut && len(b.out) > before {
		b.cutAt = b.written + cutPoint(b.out[before:])
	}
	srv.stats.finished++
	code := ""
	if e != nil {
		code = e.code
		srv.stats.errors++
		b.emit(encError(nil, *e))
		b.failMessage()
	}
	srv.s.Trace(kindDone, b.id, int64(r.st.Kind), r.seq, CodeValue(code))
	srv.logStatement(b, r, code, false)
}

func (b *backend) failMessage() {
	b.msgIdx = len(b.msg)
	if b.tx == nil {
		return
	}
	if b.tx.explicit {
		b.tx.failed = true
		return
	}
	b.rollback()
}

func (b *backend) endMessage() {
	if b.tx != nil && !b.tx.explicit {
		b.commit()
	}
	b.msgImplicit = false
	b.reportChanges()
	st := b.status()
	if b.srv.cfg.plant.idleInTx && st == 'T' {
		st = 'I'
	}
	b.emit(encReady(nil, st))
	if b.armed(pointAfter) || (st != 'I' && b.armed(pointMidTx)) {
		b.after = afterDelayedReset
	}
}

func (b *backend) read(key string) (string, bool) {
	if b.tx != nil {
		for i := len(b.tx.writes) - 1; i >= 0; i-- {
			w := b.tx.writes[i]
			if w.key != key {
				continue
			}
			if w.op == KindDelete {
				return "", false
			}
			return w.value, true
		}
	}
	return b.srv.get(key)
}

func (b *backend) execute(r *run) *pgError {
	st := r.st
	if r.cancelled {
		return &pgError{severity: "ERROR", code: "57014", message: "canceling statement due to user request"}
	}
	if b.tx != nil && b.tx.failed && st.Kind != KindCommit && st.Kind != KindRollback {
		return &pgError{severity: "ERROR", code: "25P02", message: "current transaction is aborted, commands ignored until end of transaction block"}
	}
	switch st.Kind {
	case KindConst:
		b.emit(encCommand(encDataRow(nil, []byte(st.Const)), "SELECT 1"))
	case KindDivZero:
		return &pgError{severity: "ERROR", code: "22012", message: "division by zero"}
	case KindTxid:
		b.emit(encCommand(encDataRow(nil, []byte("t")), "SELECT 1"))
	case KindRead:
		b.readReply(r)
	case KindSleep:
		b.emit(encCommand(encDataRow(nil, []byte{}), "SELECT 1"))
	case KindRows:
		b.emitRows(st.Const, st.Size, st.Rows)
		b.emit(encCommand(nil, "SELECT "+strconv.FormatInt(st.Rows, 10)))
	case KindUpdate, KindInsert, KindDelete:
		return b.write(r)
	case KindSet, KindReset, KindShow, KindDiscard:
		return b.session(st)
	case KindBegin, KindCommit, KindRollback:
		b.txControl(st.Kind)
	default:
		return &pgError{severity: "ERROR", code: "42601", message: "syntax error at or near \"" + st.Word + "\""}
	}
	return nil
}

func (b *backend) readReply(r *run) {
	var out []byte
	n := int64(0)
	if r.readFound {
		if r.st.Const == "sleep" {
			out = encDataRow2(out, []byte(r.readVal), []byte("t"))
		} else {
			out = encDataRow(out, []byte(r.readVal))
		}
		n = 1
	}
	b.emit(out)
	if f := &b.srv.faults; f.bloatRows > 0 {
		b.emitRows("x", f.bloatSize, f.bloatRows)
		n += f.bloatRows
	}
	b.emit(encCommand(nil, "SELECT "+strconv.FormatInt(n, 10)))
}

func (b *backend) write(r *run) *pgError {
	st := r.st
	_, exists := b.read(st.Key)
	n := int64(1)
	tag := ""
	switch st.Kind {
	case KindInsert:
		if exists {
			return &pgError{severity: "ERROR", code: "23505", message: "duplicate key value violates unique constraint \"kv_pkey\""}
		}
		tag = "INSERT 0 1"
	case KindUpdate:
		n = boolInt(exists)
		tag = "UPDATE " + strconv.FormatInt(n, 10)
	case KindDelete:
		n = boolInt(exists)
		tag = "DELETE " + strconv.FormatInt(n, 10)
	}
	if st.Kind == KindInsert && !exists && b.tx == nil && len(b.srv.table) >= b.srv.cfg.MaxRows {
		return &pgError{severity: "ERROR", code: "53100", message: "fakepg: table full"}
	}
	if n > 0 {
		w := pendingWrite{op: st.Kind, key: st.Key, value: st.Value, text: st.Text, tag: st.Tag, stmtSeq: r.seq}
		if b.tx == nil {
			b.srv.apply(b, 0, w)
		} else {
			if len(b.tx.writes) >= MaxTxWrites {
				return &pgError{severity: "ERROR", code: "54000", message: "fakepg: too many writes in one transaction"}
			}
			b.tx.writes = append(b.tx.writes, w)
		}
	}
	b.emit(encCommand(nil, tag))
	return nil
}

func (srv *Server) apply(b *backend, txID int64, w pendingWrite) {
	times := 1
	if srv.cfg.plant.doubleApply {
		times = 2
	}
	for i := 0; i < times; i++ {
		switch w.op {
		case KindDelete:
			srv.del(w.key)
		default:
			_ = srv.put(w.key, w.value)
		}
		srv.writeSeq++
		srv.stats.writes++
		srv.writeLog.add(WriteRecord{Seq: srv.writeSeq, StmtSeq: w.stmtSeq, Backend: b.id, TxID: txID, Op: w.op,
			Key: w.key, Value: w.value, Text: w.text, Tag: w.tag, At: srv.s.Now()})
		srv.s.Trace(kindWrite, b.id, txID, w.stmtSeq, srv.writeSeq)
	}
}

func (b *backend) commit() {
	tx := b.tx
	b.tx = nil
	b.srv.stats.txOpen--
	if tx.failed {
		b.srv.stats.txRolledBack++
		b.restoreSettings(tx)
		return
	}
	b.srv.stats.txCommitted++
	for _, w := range tx.writes {
		b.srv.apply(b, tx.id, w)
	}
}

func (b *backend) rollback() {
	tx := b.tx
	b.tx = nil
	b.srv.stats.txOpen--
	b.srv.stats.txRolledBack++
	b.restoreSettings(tx)
}

func (b *backend) restoreSettings(tx *txn) {
	if tx.saved != nil {
		b.settings = tx.saved
	}
}

var (
	warnNoTx      = pgError{severity: "WARNING", code: "25P01", message: "there is no transaction in progress"}
	warnAlreadyTx = pgError{severity: "WARNING", code: "25001", message: "there is already a transaction in progress"}
)

func (b *backend) txControl(k Kind) {
	switch k {
	case KindBegin:
		switch {
		case b.tx == nil:
			b.tx = b.srv.newTx(true)
		case b.tx.explicit:
			b.emit(encNotice(nil, warnAlreadyTx))
		default:
			b.tx.explicit = true
		}
		b.emit(encCommand(nil, "BEGIN"))
	case KindCommit:
		if b.tx == nil || !b.tx.explicit {
			b.emit(encNotice(nil, warnNoTx))
		}
		tag := "COMMIT"
		if b.tx != nil {
			if b.tx.failed {
				tag = "ROLLBACK"
			}
			b.commit()
		}
		b.emit(encCommand(nil, tag))
	case KindRollback:
		if b.tx == nil || !b.tx.explicit {
			b.emit(encNotice(nil, warnNoTx))
		}
		if b.tx != nil {
			b.rollback()
		}
		b.emit(encCommand(nil, "ROLLBACK"))
	}
}

func (srv *Server) logStatement(b *backend, r *run, code string, aborted bool) {
	srv.stmtLog.add(StatementRecord{Seq: r.seq, Backend: b.id, TxID: r.txID, Kind: r.st.Kind, Tag: r.st.Tag,
		Text: r.st.Text, Start: r.start, End: srv.s.Now(), Code: code, Aborted: aborted})
}

func (srv *Server) cancel(pid uint32, secret []byte) {
	srv.stats.cancelsReceived++
	t := srv.byKey(pid)
	switch {
	case t == nil || string(t.secret) != string(secret):
		srv.stats.cancelsNoMatch++
		srv.s.Trace(kindCancel, int64(pid), 0, 0, 0)
	case t.cur == nil || t.cur.state == runDone:
		srv.stats.cancelsIdle++
		srv.s.Trace(kindCancel, int64(pid), t.id, 1, 0)
	default:
		srv.stats.cancelsApplied++
		srv.s.Trace(kindCancel, int64(pid), t.id, 2, t.cur.seq)
		t.cur.cancelled = true
		t.leaveService(t.cur)
		t.advance()
	}
}

func (b *backend) garbled() bool {
	f := &b.srv.faults
	switch {
	case f.garbageOn || b.garbage:
		b.garbage = false
		b.srv.s.Trace(kindFire, b.id, int64(FaultGarbage), f.garbageMode, 0)
		b.emit(b.garbageBytes(f.garbageMode))
	case f.notPostgres:
		b.srv.s.Trace(kindFire, b.id, int64(FaultNotPostgres), 0, 0)
		b.emit([]byte("HTTP/1.1 400 Bad Request\r\nContent-Length: 0\r\nConnection: close\r\n\r\n"))
	default:
		return false
	}
	b.msg = nil
	b.after = afterClose
	return true
}

func (b *backend) garbageBytes(mode int64) []byte {
	switch mode {
	case 1:
		return []byte{'Z', 0, 0, 0, 2}
	case 2:
		return []byte{'D', 0x7f, 0xff, 0xff, 0xf0, 0, 1}
	}
	out := make([]byte, 64)
	for i := range out {
		out[i] = byte(b.stream.Uint32())
	}
	return out
}

func segLen(seg segment) int64 {
	if seg.rows > 0 {
		return rowHeaderLen(seg.size)
	}
	return int64(len(seg.data))
}

func cutPoint(segs []segment) int64 {
	if len(segs) > 1 {
		return segLen(segs[0]) + 1
	}
	return max(1, segLen(segs[0])/2)
}
