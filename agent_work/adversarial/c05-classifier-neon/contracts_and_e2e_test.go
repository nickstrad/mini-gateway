package adv_test

import (
	"bytes"
	"encoding/csv"
	"encoding/json"
	"go/ast"
	"go/parser"
	"go/token"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"strconv"
	"strings"
	"testing"

	"github.com/nickstrad/mini-gateway/internal/obs/simrun"
	"github.com/nickstrad/mini-gateway/internal/pgwire"
	"github.com/nickstrad/mini-gateway/internal/pgwire/pgwiretest/clsworld"
	"github.com/nickstrad/mini-gateway/internal/sim"
	"github.com/nickstrad/mini-gateway/internal/sim/fakepg"
	"github.com/nickstrad/mini-gateway/internal/sim/simnet"
	"github.com/nickstrad/mini-gateway/internal/sim/workload"
	"github.com/nickstrad/mini-gateway/internal/work"
)

// --- Claim 8: Every proxy-made reply is well formed ---

func TestAdv_8_AllRowsWireDecodeAndEndMarkers(t *testing.T) {
	_ = advSeed(t)

	var rb pgwire.ReplyBuilder
	expectedBuilt := int64(0)
	for id := pgwire.ErrRowID(0); id < pgwire.ErrRowCount; id++ {
		row := pgwire.ErrorRows[id]
		if row.Reply == pgwire.ErrorRowPassThrough {
			continue
		}
		for _, st := range []byte{'I', 'T', 'E'} {
			prefix := []byte{0xDE, 0xAD, 0xBE, 0xEF}
			out, act, err := rb.Build(append([]byte(nil), prefix...), id, st)
			if err != nil {
				t.Fatalf("row=%s status=%c: unexpected error %v", row.ID, st, err)
			}
			expectedBuilt++
			if !bytes.Equal(out[:len(prefix)], prefix) {
				t.Fatalf("row=%s status=%c: prefix mutated", row.ID, st)
			}
			frames := decodeWireFrames(t, out[len(prefix):])
			switch row.Reply {
			case pgwire.ErrorRowCloseOnly:
				if len(frames) != 0 || act.End != pgwire.ReplyEndNone || !act.Close {
					t.Errorf("close-only row=%s: frames=%d act=%+v", row.ID, len(frames), act)
				}
			case pgwire.ErrorRowFatal:
				if len(frames) != 1 || frames[0].typ != 'E' || act.End != pgwire.ReplyEndNone || !act.Close {
					t.Errorf("FATAL row=%s: frames=%d act=%+v", row.ID, len(frames), act)
				}
				fields := decodeErrorFields(t, frames[0].body)
				if fields['S'] != "FATAL" || fields['V'] != "FATAL" || fields['C'] != row.Code ||
					fields['M'] != row.Message || fields['D'] != pgwire.ReplyDetailPrefix+row.ID {
					t.Errorf("FATAL row=%s bad fields: %v", row.ID, fields)
				}
			case pgwire.ErrorRowErrorUntilSync:
				if len(frames) != 1 || frames[0].typ != 'E' || act.End != pgwire.ReplyEndSync || act.Close {
					t.Errorf("until-sync row=%s: frames=%d act=%+v", row.ID, len(frames), act)
				}
				fields := decodeErrorFields(t, frames[0].body)
				if fields['S'] != "ERROR" || fields['V'] != "ERROR" || fields['C'] != row.Code ||
					fields['M'] != row.Message || fields['D'] != pgwire.ReplyDetailPrefix+row.ID {
					t.Errorf("until-sync row=%s bad fields: %v", row.ID, fields)
				}
			case pgwire.ErrorRowError:
				if len(frames) != 2 || frames[0].typ != 'E' || frames[1].typ != 'Z' ||
					len(frames[1].body) != 1 || frames[1].body[0] != st || act.End != pgwire.ReplyEndReady {
					t.Errorf("ERROR row=%s status=%c: frames=%+v act=%+v", row.ID, st, frames, act)
				}
				wantClose := row.Conn == pgwire.ErrorRowConnClosedWhenIdle && st == 'I'
				if act.Close != wantClose {
					t.Errorf("ERROR row=%s status=%c: Close=%v, want %v", row.ID, st, act.Close, wantClose)
				}
				fields := decodeErrorFields(t, frames[0].body)
				if fields['S'] != "ERROR" || fields['V'] != "ERROR" || fields['C'] != row.Code ||
					fields['M'] != row.Message || fields['D'] != pgwire.ReplyDetailPrefix+row.ID {
					t.Errorf("ERROR row=%s bad fields: %v", row.ID, fields)
				}
			default:
				t.Fatalf("unhandled row.Reply=%d for %s", row.Reply, row.ID)
			}
		}
	}

	snap := rb.Snapshot()
	var sum int64
	for _, n := range snap.ByRow {
		sum += n
	}
	if snap.Total != expectedBuilt || sum != snap.Total || snap.Failed != 0 {
		t.Errorf("reply counts mismatch: Total=%d sum=%d expected=%d Failed=%d", snap.Total, sum, expectedBuilt, snap.Failed)
	}
}

func TestAdv_8_FailedBuildDoesNotInflateBuiltCounts(t *testing.T) {
	_ = advSeed(t)

	// When ReplyBuilder.Build refuses bad input (ErrRowPassThrough or an invalid status byte)
	// and emits NO reply bytes, it increments Failed (exported as reply_failures) — it must NOT
	// simultaneously count the refused call as a built reply in Total (replies_built) / ByRow!
	var rb pgwire.ReplyBuilder
	if out, _, err := rb.Build(nil, pgwire.ErrRowPassThrough, 'I'); err != pgwire.ReplyErrNoMessage || len(out) != 0 {
		t.Fatalf("ErrRowPassThrough: err=%v len=%d", err, len(out))
	}
	if out, _, err := rb.Build(nil, pgwire.ErrRowShed, 'X'); err != pgwire.ReplyErrBadStatus || len(out) != 0 {
		t.Fatalf("bad status: err=%v len=%d", err, len(out))
	}
	if out, _, err := rb.Build(nil, pgwire.ErrRowCount, 'I'); err != pgwire.ReplyErrBadRow || len(out) != 0 {
		t.Fatalf("bad row: err=%v len=%d", err, len(out))
	}
	snap := rb.Snapshot()
	if snap.Failed != 3 {
		t.Fatalf("Failed=%d, want 3", snap.Failed)
	}
	if snap.Total != 0 || snap.ByRow[pgwire.ErrRowPassThrough] != 0 || snap.ByRow[pgwire.ErrRowShed] != 0 {
		t.Errorf("CLAIM 8 BROKEN: refused Build calls emitted 0 bytes yet inflated built-reply counters: Total=%d ByRow[pass-through]=%d ByRow[shed]=%d (Failed=%d)",
			snap.Total, snap.ByRow[pgwire.ErrRowPassThrough], snap.ByRow[pgwire.ErrRowShed], snap.Failed)
	}
}

func TestAdv_8_SyncDiscardStateMachineExhaustive(t *testing.T) {
	_ = advSeed(t)

	// Exhaustive 256-byte check when idle and when active.
	for b := 0; b < 256; b++ {
		c := byte(b)
		var idle pgwire.SyncDiscard
		step := idle.Step(c)
		switch c {
		case 'S', 'F':
			if step != pgwire.SyncDiscardRefuseAndReady || idle.Active() || idle.Snapshot().Refused != 1 {
				t.Errorf("idle %q: step=%d active=%v snap=%+v", c, step, idle.Active(), idle.Snapshot())
			}
		case 'P', 'B', 'D', 'E', 'C', 'H':
			if step != pgwire.SyncDiscardRefuse || !idle.Active() || idle.Snapshot().Refused != 1 {
				t.Errorf("idle %q: step=%d active=%v snap=%+v", c, step, idle.Active(), idle.Snapshot())
			}
		default:
			if step != pgwire.SyncDiscardPass || idle.Active() || idle.Snapshot().Refused != 0 {
				t.Errorf("idle 0x%02x: step=%d active=%v snap=%+v", c, step, idle.Active(), idle.Snapshot())
			}
		}

		var active pgwire.SyncDiscard
		_ = active.Step('P')
		step = active.Step(c)
		switch c {
		case 'S':
			if step != pgwire.SyncDiscardReady || active.Active() || active.Snapshot().Synced != 1 {
				t.Errorf("active 'S': step=%d active=%v snap=%+v", step, active.Active(), active.Snapshot())
			}
		case 'X':
			if step != pgwire.SyncDiscardPass || active.Active() {
				t.Errorf("active 'X': step=%d active=%v", step, active.Active())
			}
		default:
			if step != pgwire.SyncDiscardDrop || !active.Active() || active.Snapshot().Discarded != 1 {
				t.Errorf("active 0x%02x: step=%d active=%v snap=%+v", c, step, active.Active(), active.Snapshot())
			}
		}
	}

	// Combined ReplyBuilder + SyncDiscard end-to-end wire encoding for P,B,E,H,D,C,Q,S -> [E:0A000, Z:T].
	var rb pgwire.ReplyBuilder
	var sd pgwire.SyncDiscard
	var wire []byte
	for _, m := range []byte("PBEHDCQS") {
		switch sd.Step(m) {
		case pgwire.SyncDiscardRefuse:
			var err error
			wire, _, err = rb.Build(wire, pgwire.ErrRowUnsupportedExtended, 'T')
			if err != nil {
				t.Fatal(err)
			}
		case pgwire.SyncDiscardReady:
			var err error
			wire, err = pgwire.ReplyReady(wire, 'T')
			if err != nil {
				t.Fatal(err)
			}
		}
	}
	frames := decodeWireFrames(t, wire)
	if len(frames) != 2 || frames[0].typ != 'E' || frames[1].typ != 'Z' || string(frames[1].body) != "T" {
		t.Fatalf("unexpected extended-protocol reply frames: %+v", frames)
	}
	fields := decodeErrorFields(t, frames[0].body)
	if fields['C'] != "0A000" {
		t.Fatalf("SQLSTATE=%q, want 0A000", fields['C'])
	}
}

func TestAdv_8_BufferReuseAndReplyReadyBoundaries(t *testing.T) {
	rng := advRNG(t, 804)
	var rb pgwire.ReplyBuilder

	// Check ReplyReady accepts ONLY 'I', 'T', 'E' across all 256 byte values.
	for b := 0; b < 256; b++ {
		c := byte(b)
		buf := []byte("pre")
		out, err := pgwire.ReplyReady(buf, c)
		valid := c == 'I' || c == 'T' || c == 'E'
		if valid {
			if err != nil || !bytes.Equal(out, []byte{'p', 'r', 'e', 'Z', 0, 0, 0, 5, c}) {
				t.Errorf("ReplyReady(%q): err=%v out=%x", c, err, out)
			}
		} else {
			if err != pgwire.ReplyErrBadStatus || !bytes.Equal(out, buf) {
				t.Errorf("ReplyReady(0x%02x): err=%v out=%x", c, err, out)
			}
		}
	}

	// Reuse a pre-allocated buffer with sentinel tail across 500 interleaved valid/invalid builds.
	backing := make([]byte, 0, 4096)
	for i := 0; i < 500; i++ {
		id := pgwire.ErrRowID(rng.IntN(int(pgwire.ErrRowCount) + 2))
		st := []byte{'I', 'T', 'E', 'X', 0}[rng.IntN(5)]
		dst := backing[:0]
		dst = append(dst, 0x42)
		out, _, err := rb.Build(dst, id, st)
		if err != nil {
			if !bytes.Equal(out, []byte{0x42}) {
				t.Fatalf("iter=%d failed Build modified dst: %x", i, out)
			}
			continue
		}
		if out[0] != 0x42 {
			t.Fatalf("iter=%d Build overwrote prefix", i)
		}
		_ = decodeWireFrames(t, out[1:])
	}
}

// --- Claim 9: Generated documents cannot drift ---

func TestAdv_9_CommittedDocsExactMatchGenerators(t *testing.T) {
	_ = advSeed(t)
	root := repoRoot(t)
	docs := map[string]string{
		"error-contract.md": pgwire.ErrorRowsMarkdown(),
		"reject-list.md":    pgwire.RejectListMarkdown(),
		"safe-shape.md":     pgwire.SafeShapeMarkdown(),
	}
	for name, want := range docs {
		got, err := os.ReadFile(filepath.Join(root, "docs", name))
		if err != nil {
			t.Fatalf("read docs/%s: %v", name, err)
		}
		if string(got) != want {
			t.Errorf("docs/%s drifted from code generator (disk=%d bytes, generated=%d bytes)", name, len(got), len(want))
		}
	}
}

func TestAdv_9_GenDocsScriptCheckAndMutationSensitivity(t *testing.T) {
	_ = advSeed(t)
	root := repoRoot(t)

	// 1. Run scripts/gen-docs.sh --check as an external process.
	cmd := exec.Command("bash", filepath.Join(root, "scripts", "gen-docs.sh"), "--check")
	cmd.Dir = root
	cmd.Env = append(os.Environ(), "PATH=/usr/local/go/bin:"+os.Getenv("PATH"))
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("scripts/gen-docs.sh --check failed: %v\n%s", err, out)
	}

	// 2. Mutate EVERY underlying table in memory (restoring via defer) and prove each
	//    generator reflects the mutation immediately.
	t.Run("ErrorRows", func(t *testing.T) {
		orig := pgwire.ErrorRows[pgwire.ErrRowLostAfterRead]
		defer func() { pgwire.ErrorRows[pgwire.ErrRowLostAfterRead] = orig }()
		pgwire.ErrorRows[pgwire.ErrRowLostAfterRead].Situation = "nadv-err-sentinel"
		if !strings.Contains(pgwire.ErrorRowsMarkdown(), "nadv-err-sentinel") {
			t.Error("ErrorRowsMarkdown ignored ErrorRows mutation")
		}
	})
	t.Run("RejectRules", func(t *testing.T) {
		orig := pgwire.RejectRules[0]
		defer func() { pgwire.RejectRules[0] = orig }()
		pgwire.RejectRules[0].Example = "SET nadv_sentinel = 1"
		if !strings.Contains(pgwire.RejectListMarkdown(), "SET nadv_sentinel = 1") {
			t.Error("RejectListMarkdown ignored RejectRules mutation")
		}
	})
	t.Run("RejectAllowedForms", func(t *testing.T) {
		orig := pgwire.RejectAllowedForms[0]
		defer func() { pgwire.RejectAllowedForms[0] = orig }()
		pgwire.RejectAllowedForms[0].Why = "nadv-allowed-sentinel"
		if !strings.Contains(pgwire.RejectListMarkdown(), "nadv-allowed-sentinel") {
			t.Error("RejectListMarkdown ignored RejectAllowedForms mutation")
		}
	})
	t.Run("ShareTables", func(t *testing.T) {
		origStop := pgwire.ShareStopWords[0]
		origParen := pgwire.ShareParenKeywords[0]
		origAllow := pgwire.ShareAllowList[0]
		origTime := pgwire.ShareTimeWords[0]
		defer func() {
			pgwire.ShareStopWords[0] = origStop
			pgwire.ShareParenKeywords[0] = origParen
			pgwire.ShareAllowList[0] = origAllow
			pgwire.ShareTimeWords[0] = origTime
		}()
		pgwire.ShareStopWords[0] = "nadv_stop_sentinel"
		pgwire.ShareParenKeywords[0] = "nadv_paren_sentinel"
		pgwire.ShareAllowList[0] = "nadv_allow_sentinel"
		pgwire.ShareTimeWords[0] = "nadv_time_sentinel"
		doc := pgwire.SafeShapeMarkdown()
		for _, s := range []string{"nadv_stop_sentinel", "nadv_paren_sentinel", "nadv_allow_sentinel", "nadv_time_sentinel"} {
			if !strings.Contains(doc, s) {
				t.Errorf("SafeShapeMarkdown ignored table mutation %q", s)
			}
		}
	})
}

func TestAdv_9_SafeShapeAndContractDocsCompleteness(t *testing.T) {
	_ = advSeed(t)
	root := repoRoot(t)

	safeDocBytes, err := os.ReadFile(filepath.Join(root, "docs", "safe-shape.md"))
	if err != nil {
		t.Fatal(err)
	}
	safeDoc := string(safeDocBytes)
	for _, sw := range briefStopWords {
		if !strings.Contains(safeDoc, "`"+sw+"`") {
			t.Errorf("docs/safe-shape.md missing required stop word %q", sw)
		}
	}
	for _, must := range []string{strconv.Itoa(pgwire.ShareMaxLen), "`/* mg:share */`", "`now`", "`today`", "`tomorrow`", "`yesterday`"} {
		if !strings.Contains(safeDoc, must) {
			t.Errorf("docs/safe-shape.md missing %s", must)
		}
	}

	errDocBytes, err := os.ReadFile(filepath.Join(root, "docs", "error-contract.md"))
	if err != nil {
		t.Fatal(err)
	}
	errDoc := string(errDocBytes)
	for id := pgwire.ErrRowID(0); id < pgwire.ErrRowCount; id++ {
		row := pgwire.ErrorRows[id]
		if !strings.Contains(errDoc, "`"+row.ID+"`") {
			t.Errorf("docs/error-contract.md missing row %q", row.ID)
		}
	}

	rejDocBytes, err := os.ReadFile(filepath.Join(root, "docs", "reject-list.md"))
	if err != nil {
		t.Fatal(err)
	}
	rejDoc := string(rejDocBytes)
	for r := work.RejectSessionSet; r < work.NumRejectReasons; r++ {
		if !strings.Contains(rejDoc, "`"+r.String()+"`") {
			t.Errorf("docs/reject-list.md missing reject reason %q", r.String())
		}
	}
}

// --- Claim 10: internal/work imports only stdlib; output in its types ---

func TestAdv_10_WorkASTAndBuildClosureStandardOnly(t *testing.T) {
	_ = advSeed(t)
	root := repoRoot(t)
	workDir := filepath.Join(root, "internal", "work")

	files, err := filepath.Glob(filepath.Join(workDir, "*.go"))
	if err != nil || len(files) == 0 {
		t.Fatalf("no Go files in internal/work: %v", err)
	}
	fset := token.NewFileSet()
	for _, fpath := range files {
		af, err := parser.ParseFile(fset, fpath, nil, parser.ImportsOnly)
		if err != nil {
			t.Fatal(err)
		}
		for _, imp := range af.Imports {
			p := strings.Trim(imp.Path.Value, `"`)
			first := strings.SplitN(p, "/", 2)[0]
			if strings.Contains(first, ".") {
				t.Errorf("%s imports non-stdlib package %q", filepath.Base(fpath), p)
			}
		}
	}

	// Verify full transitive dependency closure via `go list -deps -json`.
	cmd := exec.Command("go", "list", "-deps", "-json", "./internal/work")
	cmd.Dir = root
	cmd.Env = append(os.Environ(), "PATH=/usr/local/go/bin:"+os.Getenv("PATH"))
	out, err := cmd.Output()
	if err != nil {
		t.Fatalf("go list -deps failed: %v", err)
	}
	dec := json.NewDecoder(bytes.NewReader(out))
	pkgs := 0
	for dec.More() {
		var pkg struct {
			ImportPath string
			Standard   bool
		}
		if err := dec.Decode(&pkg); err != nil {
			t.Fatal(err)
		}
		pkgs++
		if !pkg.Standard && pkg.ImportPath != "github.com/nickstrad/mini-gateway/internal/work" {
			t.Errorf("internal/work transitive dependency %q is not in the standard library", pkg.ImportPath)
		}
	}
	if pkgs == 0 {
		t.Fatal("go list returned zero packages")
	}
}

func TestAdv_10_NoPostgresSymbolsOrIdentifiersInWork(t *testing.T) {
	_ = advSeed(t)
	workDir := filepath.Join(repoRoot(t), "internal", "work")
	entries, err := os.ReadDir(workDir)
	if err != nil {
		t.Fatal(err)
	}
	forbidden := []string{"pgproto3", "pgwire", "pgx", "sqlstate", "errorresponse", "readyforquery", "postgres", "psql", "0a000", "53m01"}
	fset := token.NewFileSet()
	for _, e := range entries {
		if e.IsDir() || !strings.HasSuffix(e.Name(), ".go") || strings.HasSuffix(e.Name(), "_test.go") {
			continue
		}
		src, err := os.ReadFile(filepath.Join(workDir, e.Name()))
		if err != nil {
			t.Fatal(err)
		}
		af, err := parser.ParseFile(fset, e.Name(), src, parser.ParseComments)
		if err != nil {
			t.Fatal(err)
		}
		ast.Inspect(af, func(n ast.Node) bool {
			var txt string
			switch x := n.(type) {
			case *ast.Ident:
				txt = x.Name
			case *ast.BasicLit:
				txt = x.Value
			default:
				return true
			}
			low := strings.ToLower(txt)
			for _, bad := range forbidden {
				if strings.Contains(low, bad) {
					t.Errorf("internal/work/%s contains Postgres-specific token %q (matched %q)", e.Name(), txt, bad)
				}
			}
			return true
		})
	}
}

func TestAdv_10_ClassifierPublicAPIReturnTypesWorkOnly(t *testing.T) {
	_ = advSeed(t)
	const workPkg = "github.com/nickstrad/mini-gateway/internal/work"

	seen := map[reflect.Type]bool{}
	var walk func(t *testing.T, rt reflect.Type, path string)
	walk = func(t *testing.T, rt reflect.Type, path string) {
		if rt == nil || seen[rt] {
			return
		}
		seen[rt] = true
		if pkg := rt.PkgPath(); pkg != "" && pkg != workPkg {
			t.Errorf("classifier output path %s uses type %s from foreign package %q", path, rt, pkg)
		}
		switch rt.Kind() {
		case reflect.Struct:
			for i := 0; i < rt.NumField(); i++ {
				f := rt.Field(i)
				walk(t, f.Type, path+"."+f.Name)
			}
		case reflect.Slice, reflect.Array, reflect.Pointer, reflect.Chan:
			walk(t, rt.Elem(), path+"[]")
		case reflect.Map:
			walk(t, rt.Key(), path+".key")
			walk(t, rt.Elem(), path+".val")
		}
	}

	cqType := reflect.TypeOf(pgwire.ClassifyQuery)
	if cqType.NumOut() != 1 {
		t.Fatalf("ClassifyQuery returns %d values, want 1", cqType.NumOut())
	}
	walk(t, cqType.Out(0), "ClassifyQuery.ret0")

	var c pgwire.Classifier
	mType := reflect.TypeOf(c.Classify)
	if mType.NumOut() != 1 {
		t.Fatalf("Classifier.Classify returns %d values, want 1", mType.NumOut())
	}
	walk(t, mType.Out(0), "Classifier.Classify.ret0")
}

// --- Claim 11: Refused statements never reach the database, and the flushed files say so ---

func TestAdv_11_HostileRejectsNeverReachFakeWithFragmentation(t *testing.T) {
	seed := advSeed(t)

	s := sim.New(sim.Config{Seed: int64(seed), Scenario: "nadv-frag"})
	n := simnet.New(s)
	srv, err := fakepg.New(n, fakepg.Config{})
	if err != nil {
		t.Fatal(err)
	}
	gw, err := clsworld.NewGateway(n, srv.Addr(), pgwire.ClassifierConfig{})
	if err != nil {
		t.Fatal(err)
	}

	probe := workload.NewProbe(n, clsworld.Addr, simnet.DefaultOptions())
	probe.Startup("app", "app")
	if err := s.RunUntilQuiet(); err != nil {
		t.Fatal(err)
	}

	// Hostile rejected simple-query statements spanning all 15 SQL reasons plus tricky spellings.
	hostileRejects := []string{
		"SET search_path = evil",
		"RESET ALL",
		"PREPARE p AS SELECT 1",
		"EXECUTE p",
		"DEALLOCATE ALL",
		"LISTEN evil",
		"UNLISTEN *",
		"DECLARE c CURSOR WITH HOLD FOR SELECT 1",
		"CREATE TEMP TABLE tt (a int)",
		`SELECT U&"pg_advisory_\006cock"(42)`,
		"SELECT set_config('search_path', 'evil', false)",
		"DISCARD ALL",
		"LOAD 'evil'",
		"COPY kv TO STDOUT",
		"PREPARE TRANSACTION 'tx1'",
		"SELECT 1; SET application_name = 'hidden'",
		"/* mg:share */ LISTEN hinted_evil",
		"SELECT E'ok'\n'\\' ; SELECT ' ; SET search_path = hidden ; --'",
	}

	// Send each hostile query fragmented 1 byte at a time!
	for _, q := range hostileRejects {
		pkt := pgwire.AppendQuery(nil, []byte(q))
		for i := range pkt {
			probe.Send(pkt[i : i+1])
			if err := s.RunUntilQuiet(); err != nil {
				t.Fatal(err)
			}
		}
	}

	// Also send extended-protocol Parse + Bind + Execute + Sync AND a bare FunctionCall ('F') fragmented 1 byte at a time.
	extStream := []byte{
		'P', 0, 0, 0, 8, 0, 0, 0, 0,
		'B', 0, 0, 0, 6, 0, 0,
		'E', 0, 0, 0, 9, 0, 0, 0, 0, 0,
		'S', 0, 0, 0, 4,
		'F', 0, 0, 0, 4,
	}
	for i := range extStream {
		probe.Send(extStream[i : i+1])
		if err := s.RunUntilQuiet(); err != nil {
			t.Fatal(err)
		}
	}

	// Zero statements must have reached fakepg!
	pgSnap := srv.Snapshot()
	if pgSnap.StatementsStarted != 0 || pgSnap.WritesApplied != 0 || pgSnap.ParamReports != 0 {
		t.Fatalf("refused statements leaked to fake Postgres: started=%d writes=%d params=%d log=%+v",
			pgSnap.StatementsStarted, pgSnap.WritesApplied, pgSnap.ParamReports, srv.StatementLog())
	}

	// Verify recovery: a normal SELECT 1 still works cleanly afterwards.
	probe.Query("SELECT 1")
	if err := s.RunUntilQuiet(); err != nil {
		t.Fatal(err)
	}
	if srv.Snapshot().StatementsStarted != 1 {
		t.Fatalf("recovery query failed: StatementsStarted=%d, want 1", srv.Snapshot().StatementsStarted)
	}
	probe.Terminate()
	probe.Close()
	if err := s.RunUntilQuiet(); err != nil {
		t.Fatal(err)
	}
	if err := s.CheckQuiesce(); err != nil {
		t.Fatalf("quiesce failed: %v", err)
	}
	cs := gw.Classifier.Snapshot()
	if err := cs.Check(); err != nil {
		t.Fatalf("classifier snapshot conservation failed: %v", err)
	}
	if cs.ByClass[work.ClassReject] != int64(len(hostileRejects)) {
		t.Fatalf("classifier rejects=%d, want %d", cs.ByClass[work.ClassReject], len(hostileRejects))
	}
}

func runCSVAssert(t *testing.T, args ...string) (string, error) {
	t.Helper()
	root := repoRoot(t)
	cmd := exec.Command("bash", append([]string{filepath.Join(root, "scripts", "csv-assert.sh")}, args...)...)
	cmd.Dir = root
	home, _ := os.UserHomeDir()
	cmd.Env = append(os.Environ(), "PATH=/usr/local/go/bin:"+home+"/.local/bin:"+os.Getenv("PATH"))
	out, err := cmd.CombinedOutput()
	return string(out), err
}

func readFinalMetricSums(t *testing.T, runDir string) map[string]int64 {
	t.Helper()
	f, err := os.Open(filepath.Join(runDir, "metrics.csv"))
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	recs, err := csv.NewReader(f).ReadAll()
	if err != nil {
		t.Fatal(err)
	}
	if len(recs) < 2 {
		t.Fatalf("metrics.csv has only %d rows", len(recs))
	}
	maxSeq := int64(-1)
	for _, r := range recs[1:] {
		seq, err := strconv.ParseInt(r[2], 10, 64)
		if err != nil {
			t.Fatal(err)
		}
		if seq > maxSeq {
			maxSeq = seq
		}
	}
	sums := map[string]int64{}
	for _, r := range recs[1:] {
		seq, _ := strconv.ParseInt(r[2], 10, 64)
		if seq != maxSeq {
			continue
		}
		val, err := strconv.ParseInt(r[12], 10, 64)
		if err != nil {
			t.Fatal(err)
		}
		key := r[5] + "." + r[6]
		sums[key] += val
	}
	return sums
}

func TestAdv_11_FlushedCSVMatchesSnapshotsAndSQLAssertionsPass(t *testing.T) {
	seed := advSeed(t)
	dir1 := t.TempDir()
	dir2 := t.TempDir()

	for _, sc := range clsworld.Scenarios() {
		for offset := int64(0); offset < 3; offset++ {
			s := int64(seed) + offset
			r1, err := simrun.Classifier(sc, s, simrun.Options{Dir: dir1})
			if err != nil {
				t.Fatalf("%s seed=%d: %v", sc.Name, s, err)
			}
			if err := r1.Classifier.Verify(); err != nil {
				t.Fatalf("%s seed=%d Verify: %v", sc.Name, s, err)
			}
			if err := r1.Check(); err != nil {
				t.Fatalf("%s seed=%d Check: %v", sc.Name, s, err)
			}

			// Independent CSV parse verification.
			sums := readFinalMetricSums(t, r1.RunDir)
			sent := sums["wire.client_queries_seen"]
			rejects := sums["classifier.rejects"]
			classified := sums["classifier.statements"]
			started := sums["fakepg.statements_started"]
			if classified != sent || sent < 1 {
				t.Fatalf("%s seed=%d: classified=%d sent=%d", sc.Name, s, classified, sent)
			}
			if sc.Name == clsworld.NameRejectOnly {
				if rejects != sent || started != 0 {
					t.Fatalf("%s seed=%d: rejects=%d sent=%d started=%d", sc.Name, s, rejects, sent, started)
				}
			} else {
				if started != classified-rejects || started < 1 || rejects < 1 {
					t.Fatalf("%s seed=%d: started=%d classified=%d rejects=%d", sc.Name, s, started, classified, rejects)
				}
			}

			// Replay determinism: second run must produce byte-identical CSV files.
			r2, err := simrun.Classifier(sc, s, simrun.Options{Dir: dir2})
			if err != nil {
				t.Fatalf("replay %s seed=%d: %v", sc.Name, s, err)
			}
			for _, fn := range []string{"metrics.csv", "histograms.csv", "run.csv"} {
				a, err := os.ReadFile(filepath.Join(r1.RunDir, fn))
				if err != nil {
					t.Fatal(err)
				}
				b, err := os.ReadFile(filepath.Join(r2.RunDir, fn))
				if err != nil {
					t.Fatal(err)
				}
				if !bytes.Equal(a, b) {
					t.Fatalf("%s seed=%d %s differs across runs", sc.Name, s, fn)
				}
			}
		}
	}

	// Run scripts/csv-assert.sh on all flushed classifier runs with DuckDB.
	root := repoRoot(t)
	sqlChecks := []struct {
		dir string
		sql string
	}{
		{filepath.Join(dir1, clsworld.NameRejectOnly), filepath.Join(root, "test", "analysis", clsworld.NameRejectOnly, "rejects-equal-sent.sql")},
		{filepath.Join(dir1, clsworld.NameRejectOnly), filepath.Join(root, "test", "analysis", clsworld.NameRejectOnly, "fakepg-statements-zero.sql")},
		{filepath.Join(dir1, clsworld.NameRejectList), filepath.Join(root, "test", "analysis", clsworld.NameRejectList, "forwarded-equal-started.sql")},
	}
	for _, chk := range sqlChecks {
		out, err := runCSVAssert(t, chk.dir, chk.sql)
		if err != nil {
			t.Fatalf("csv-assert failed on %s: %v\n%s", filepath.Base(chk.sql), err, out)
		}
	}
}

func TestAdv_11_AblationAndSQLFailClosedOnTamperedFiles(t *testing.T) {
	seed := advSeed(t)
	root := repoRoot(t)

	// 1. Run a fresh reject-only scenario and verify clean baseline passes both SQL files.
	baseDir := t.TempDir()
	r, err := simrun.Classifier(clsworld.RejectOnly(), int64(seed), simrun.Options{Dir: baseDir})
	if err != nil {
		t.Fatal(err)
	}
	sqlRejects := filepath.Join(root, "test", "analysis", clsworld.NameRejectOnly, "rejects-equal-sent.sql")
	sqlZero := filepath.Join(root, "test", "analysis", clsworld.NameRejectOnly, "fakepg-statements-zero.sql")
	if out, err := runCSVAssert(t, r.RunDir, sqlRejects, sqlZero); err != nil {
		t.Fatalf("clean baseline failed: %v\n%s", err, out)
	}

	copyDir := func(t *testing.T) string {
		t.Helper()
		dst := t.TempDir()
		for _, fn := range []string{"metrics.csv", "histograms.csv", "run.csv"} {
			b, err := os.ReadFile(filepath.Join(r.RunDir, fn))
			if err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(filepath.Join(dst, fn), b, 0o644); err != nil {
				t.Fatal(err)
			}
		}
		return dst
	}

	// 2. Missing run.csv MUST fail closed.
	t.Run("missing_run_csv", func(t *testing.T) {
		d := copyDir(t)
		_ = os.Remove(filepath.Join(d, "run.csv"))
		if out, err := runCSVAssert(t, d, sqlRejects); err == nil {
			t.Fatalf("csv-assert passed without run.csv:\n%s", out)
		}
	})

	// 3. Wrong schema_version MUST fail closed.
	t.Run("bad_schema_version", func(t *testing.T) {
		d := copyDir(t)
		b, _ := os.ReadFile(filepath.Join(d, "run.csv"))
		lines := strings.Split(string(b), "\n")
		lines[1] = "2" + lines[1][1:]
		_ = os.WriteFile(filepath.Join(d, "run.csv"), []byte(strings.Join(lines, "\n")), 0o644)
		if out, err := runCSVAssert(t, d, sqlRejects); err == nil {
			t.Fatalf("csv-assert passed with schema_version=2:\n%s", out)
		}
	})

	// 4. Simulated leaked statement (fakepg.statements_started = 1 and session-set rejects -= 1)
	//    MUST fail BOTH rejects-equal-sent.sql AND fakepg-statements-zero.sql.
	t.Run("simulated_leaked_reject", func(t *testing.T) {
		d := copyDir(t)
		b, _ := os.ReadFile(filepath.Join(d, "metrics.csv"))
		lines := strings.Split(strings.TrimRight(string(b), "\n"), "\n")
		maxSeq := -1
		for _, ln := range lines[1:] {
			cols := strings.Split(ln, ",")
			seq, _ := strconv.Atoi(cols[2])
			if seq > maxSeq {
				maxSeq = seq
			}
		}
		maxSeqStr := strconv.Itoa(maxSeq)
		for i := 1; i < len(lines); i++ {
			cols := strings.Split(lines[i], ",")
			if cols[2] != maxSeqStr {
				continue
			}
			if cols[5] == "fakepg" && cols[6] == "statements_started" {
				cols[12] = "1"
				lines[i] = strings.Join(cols, ",")
			}
			if cols[5] == "classifier" && cols[6] == "rejects" && cols[10] == "session-set" {
				v, _ := strconv.Atoi(cols[12])
				cols[12] = strconv.Itoa(v - 1)
				lines[i] = strings.Join(cols, ",")
			}
		}
		_ = os.WriteFile(filepath.Join(d, "metrics.csv"), []byte(strings.Join(lines, "\n")+"\n"), 0o644)
		for _, sql := range []string{sqlRejects, sqlZero} {
			out, err := runCSVAssert(t, d, sql)
			if err == nil {
				t.Fatalf("%s passed despite leaked statement:\n%s", filepath.Base(sql), out)
			}
			if !strings.Contains(out, "violating rows") {
				t.Fatalf("%s failed without 'violating rows':\n%s", filepath.Base(sql), out)
			}
		}
	})

	// 5. Wrong scenario name (keeping run_id consistent across files) MUST fail closed with
	//    "no run of classifier-reject-only" violating row.
	t.Run("wrong_scenario", func(t *testing.T) {
		d := copyDir(t)
		b, _ := os.ReadFile(filepath.Join(d, "run.csv"))
		lines := strings.Split(strings.TrimRight(string(b), "\n"), "\n")
		cols := strings.Split(lines[1], ",")
		cols[2] = "other-scenario"
		lines[1] = strings.Join(cols, ",")
		_ = os.WriteFile(filepath.Join(d, "run.csv"), []byte(strings.Join(lines, "\n")+"\n"), 0o644)
		out, err := runCSVAssert(t, d, sqlRejects)
		if err == nil || !strings.Contains(out, "violating rows") {
			t.Fatalf("wrong scenario did not fail with violating rows: err=%v\n%s", err, out)
		}
	})
}

func TestAdv_11_PlantedBugCanaryRemainsUnreachableFromExternalPackages(t *testing.T) {
	_ = advSeed(t)

	// Verify the builder's planted-SET canary is enforced (and passes when run in pgwire)
	// while remaining unexported from internal/pgwire's non-test API.
	cfgType := reflect.TypeOf(pgwire.ClassifierConfig{})
	for i := 0; i < cfgType.NumField(); i++ {
		f := cfgType.Field(i)
		if strings.Contains(strings.ToLower(f.Name), "plant") && f.IsExported() {
			t.Errorf("ClassifierConfig exposes planted-bug field %q as exported!", f.Name)
		}
	}
	root := repoRoot(t)
	cmd := exec.Command("go", "test", "-count=1", "-run", "^TestClassifierPlantedSetForwardableIsCaught$", "./internal/pgwire")
	cmd.Dir = root
	cmd.Env = append(os.Environ(), "PATH=/usr/local/go/bin:"+os.Getenv("PATH"))
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("planted-SET canary failed: %v\n%s", err, out)
	}
}
