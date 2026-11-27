package adv_test

import (
	"bytes"
	"context"
	"crypto/sha1"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"
	"sync"
	"testing"
	"time"
)

const seed = 271828
const area = "agent_work/adversarial/lease-lab-port"
const records = "agent_work/chunks/c05-classifier/lease-exp"

var root, scratch string

func TestMain(m *testing.M) {
	wd, _ := os.Getwd()
	root = filepath.Clean(filepath.Join(wd, "../../.."))
	scratch = filepath.Join(root, area, ".scratch")
	if err := os.MkdirAll(scratch, 0755); err != nil {
		panic(err)
	}
	os.Setenv("PATH", "/usr/local/go/bin:"+os.Getenv("PATH"))
	os.Setenv("TMPDIR", scratch)
	code := m.Run()
	os.RemoveAll(scratch)
	os.Exit(code)
}
func read(t *testing.T, p string) string {
	t.Helper()
	b, e := os.ReadFile(filepath.Join(root, p))
	if e != nil {
		t.Fatal(e)
	}
	return string(b)
}
func begin(t *testing.T) { t.Helper(); t.Logf("seed=%d", seed) }
func long(t *testing.T) {
	t.Helper()
	begin(t)
	if os.Getenv("ADV_LONG") != "1" {
		t.Skip("mutation/load campaign: ADV_LONG=1")
	}
}
func run(t *testing.T, dir string, env []string, name string, args ...string) (string, error) {
	t.Helper()
	limit := 150 * time.Second
	if os.Getenv("ADV_LONG") == "1" {
		limit = 21 * time.Minute
	}
	ctx, cancel := context.WithTimeout(context.Background(), limit)
	defer cancel()
	c := exec.CommandContext(ctx, name, args...)
	c.Dir = dir
	c.Env = append(os.Environ(), env...)
	b, e := c.CombinedOutput()
	if ctx.Err() != nil {
		t.Fatalf("deadline: %s %v: %s", name, args, b)
	}
	return string(b), e
}
func save(t *testing.T, text string) {
	t.Helper()
	p := filepath.Join(root, area, "evidence")
	os.MkdirAll(p, 0755)
	f, err := os.OpenFile(filepath.Join(p, t.Name()+".log"), os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0644)
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	if _, e := f.WriteString(text); e != nil {
		t.Fatal(e)
	}
}
func child(t *testing.T, changes map[string]string, inject, pattern string, real bool, count int) (string, error) {
	t.Helper()
	dir, e := os.MkdirTemp(scratch, "overlay-")
	if e != nil {
		t.Fatal(e)
	}
	defer os.RemoveAll(dir)
	repl := map[string]string{}
	if changes == nil {
		changes = map[string]string{}
	}
	if inject != "" {
		p := "test/e2e/tierr_test.go"
		s, ok := changes[p]
		if !ok {
			s = read(t, p)
		}
		changes[p] = s + "\n" + inject
	}
	for p, s := range changes {
		dest := filepath.Join(dir, fmt.Sprintf("%d.go", len(repl)))
		if e := os.WriteFile(dest, []byte(s), 0644); e != nil {
			t.Fatal(e)
		}
		repl[filepath.Join(root, p)] = dest
	}
	b, _ := json.Marshal(map[string]any{"Replace": repl})
	ov := filepath.Join(dir, "overlay.json")
	os.WriteFile(ov, b, 0644)
	env := []string{"GOFLAGS=-overlay=" + ov, "MG_TIER_R=0", "RUN_DIR=" + filepath.Join(dir, "runs")}
	if real {
		prefix := "adv-lease-" + filepath.Base(dir)
		env = append(env, "MG_TIER_R=1", "MG_TIER_R_PREFIX="+prefix)
		defer func() {
			ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
			defer cancel()
			// TestMain cannot clean up after Go's test-timeout panic.
			exec.CommandContext(ctx, "docker", "rm", "-f", "-v", prefix+"-c06").Run()
		}()
	}
	out, err := run(t, root, env, "go", "test", "-count="+fmt.Sprint(count), "-timeout="+func() string {
		if os.Getenv("ADV_LONG") == "1" {
			return "20m"
		}
		return "140s"
	}(), "./test/e2e", "-run", pattern, "-v")
	save(t, out)
	return out, err
}
func expect(t *testing.T, out string, err error, fail bool, message string) {
	t.Helper()
	if strings.Contains(out, "[build failed]") || strings.Contains(out, "tier R setup:") || strings.Contains(out, "panic:") {
		t.Fatalf("harness/infrastructure failure: %v\n%s", err, out)
	}
	if (err != nil) != fail || !strings.Contains(out, message) {
		t.Fatalf("expected fail=%v and %q; got err=%v\n%s", fail, message, err, out)
	}
}

var batchOnce sync.Once
var batchOut string
var batchErr error

func live(t *testing.T, n string) {
	begin(t)
	batchOnce.Do(func() { batchOut, batchErr = child(t, nil, read(t, area+"/live.go.txt"), "^TestProbeLive_", true, 1) })
	if !strings.Contains(batchOut, "--- PASS: TestProbeLive_"+n+" ") {
		t.Fatalf("live probe %s failed (batch %v):\n%s", n, batchErr, batchOut)
	}
}
func TestAdv_1_CommitMixed(t *testing.T)       { live(t, "CommitMixed") }
func TestAdv_1_RollbackMixed(t *testing.T)     { live(t, "RollbackMixed") }
func TestAdv_1_SavepointMixed(t *testing.T)    { live(t, "SavepointMixed") }
func TestAdv_2_AlwaysLargeSecret(t *testing.T) { live(t, "AlwaysLargeSecret") }
func TestAdv_2_OnSwitchImmediate(t *testing.T) { live(t, "OnSwitchImmediate") }
func TestAdv_2_OnSwitchReseed(t *testing.T)    { live(t, "OnSwitchReseed") }

var blindOnce sync.Once
var blindOut string
var blindErr error

func blind(t *testing.T, name string) {
	begin(t)
	blindOnce.Do(func() {
		inject := `func advBlind(t *testing.T, got []string) {r:=sessionResult{columns: []string{"prepared","cursors","listens","adv_locks","session_gucs","temp_rels","role_changed"},moved:make([]int,7)}; r.observe([]string{"0","0","0","0","0","0","f"},got); for i,n:=range r.moved {if n!=0 {t.Errorf("column %s falsely counted as exercised (%d) by invalid observation",r.columns[i],n)}}}
func TestProbeBlindError(t *testing.T){advBlind(t,[]string{"E:42501","ZI"})}
func TestProbeBlindEmpty(t *testing.T){advBlind(t,nil)}
func TestProbeBlindTruncated(t *testing.T){advBlind(t,[]string{"D:0|0|0","ZI"})}`
		blindOut, blindErr = child(t, nil, inject, "^TestProbeBlind", false, 1)
	})
	if !strings.Contains(blindOut, "--- PASS: TestProbeBlind"+name+" ") {
		t.Fatalf("invalid observation was counted as movement (%v):\n%s", blindErr, blindOut)
	}
}
func TestAdv_3_ErrorIsNotMovement(t *testing.T)     { blind(t, "Error") }
func TestAdv_3_EmptyIsNotMovement(t *testing.T)     { blind(t, "Empty") }
func TestAdv_3_TruncatedIsNotMovement(t *testing.T) { blind(t, "Truncated") }

func omitSetup(t *testing.T, kind string) {
	long(t)
	s := read(t, "test/e2e/realenv.go")
	re := regexp.MustCompile("(?m)^.*CREATE FUNCTION do_" + kind + "\\(.*$")
	line := re.FindString(s)
	if line == "" {
		t.Fatal("function absent")
	}
	// Keep its signature/grants, but remove the body effect: setup must still succeed.
	changed := regexp.MustCompile(`BEGIN .* RETURN`).ReplaceAllString(line, "BEGIN RETURN")
	s = strings.Replace(s, line, changed, 1)
	out, e := child(t, map[string]string{"test/e2e/realenv.go": s}, "", "^TestTierRSessionAblationWithoutResetCrosses$", true, 1)
	want := map[string]string{"cursor": "column cursors", "role": "column role_changed", "custom": "was never read back"}[kind]
	expect(t, out, e, true, want)
}
func TestAdv_4_InertCursor(t *testing.T) { omitSetup(t, "cursor") }
func TestAdv_4_InertRole(t *testing.T)   { omitSetup(t, "role") }
func TestAdv_4_InertCustom(t *testing.T) { omitSetup(t, "custom") }

func resetSource(t *testing.T, sql string) string {
	s := read(t, "internal/proxy/lease.go")
	s = strings.Replace(s, `const discardAll = "DISCARD ALL"`, "const discardAll = `"+sql+"`", 1)
	marker := "func (j *resetJudge) expect"
	i := strings.Index(s, marker)
	if i < 0 {
		t.Fatal("reset judge missing")
	}
	return s[:i] + strings.Replace(s[i:], "case pgwire.MsgCommandComplete:", "case pgwire.MsgCommandComplete:\n if tag == discardAll { j.sawTag = true; break }", 1)
}

const partial = "RESET ALL; DEALLOCATE ALL; UNLISTEN *; DO $$BEGIN PERFORM pg_advisory_unlock_all(); END$$; DISCARD TEMP"
const noGUC = "CLOSE ALL; RESET ROLE; RESET search_path; DEALLOCATE ALL; UNLISTEN *; DO $$BEGIN PERFORM pg_advisory_unlock_all(); END$$; DISCARD PLANS; DISCARD TEMP; DISCARD SEQUENCES"

func resetProbe(t *testing.T, sql string, baseline bool, want string) {
	long(t)
	changes := map[string]string{"internal/proxy/lease.go": resetSource(t, sql)}
	if baseline {
		for _, p := range []string{"test/e2e/realenv.go", "test/e2e/tierr_test.go", "test/e2e/c07_tierr_test.go"} {
			s, e := run(t, root, nil, "git", "show", "98cee87:"+p)
			if e != nil {
				t.Fatal(e)
			}
			changes[p] = s
		}
		out, e := child(t, changes, "", "^TestTierRSessionStateNeverCrossesClients$", true, 1)
		expect(t, out, e, false, "--- PASS:")
		delete(changes, "test/e2e/realenv.go")
		delete(changes, "test/e2e/tierr_test.go")
		delete(changes, "test/e2e/c07_tierr_test.go")
	}
	out, e := child(t, changes, "", "^TestTierRSessionStateNeverCrossesClients$", true, 1)
	expect(t, out, e, true, want)
}
func TestAdv_5_ResetReordered(t *testing.T) {
	resetProbe(t, "DISCARD PLANS; "+partial, true, "dirty observations")
}
func TestAdv_5_ResetRepeated(t *testing.T) {
	resetProbe(t, partial+"; RESET ALL", true, "dirty observations")
}
func TestAdv_5_ResetSequences(t *testing.T) {
	resetProbe(t, partial+"; DISCARD SEQUENCES", true, "dirty observations")
}
func TestAdv_6_NamedSettingReset(t *testing.T) { resetProbe(t, noGUC, true, "secret read back") }
func TestAdv_6_NamedResetRepeated(t *testing.T) {
	resetProbe(t, noGUC+"; RESET search_path", true, "secret read back")
}
func TestAdv_6_TempPlansOnly(t *testing.T) {
	resetProbe(t, "DISCARD TEMP; DISCARD PLANS", false, "dirty observations")
}
func removeKind(t *testing.T, kind, want string) {
	long(t)
	inject := `func init(){ for i,s:=range hostileCalls {if strings.Contains(s,"do_` + kind + `("){hostileCalls=append(hostileCalls[:i],hostileCalls[i+1:]...);break}} }`
	out, e := child(t, nil, inject, "^TestTierRSessionAblationWithoutResetCrosses$", true, 1)
	expect(t, out, e, true, want)
}
func TestAdv_7_RemoveCursor(t *testing.T) { removeKind(t, "cursor", "column cursors") }
func TestAdv_7_RemoveRole(t *testing.T)   { removeKind(t, "role", "column role_changed") }
func TestAdv_7_RemoveCustom(t *testing.T) { removeKind(t, "custom", "was never read back") }
func repeat(t *testing.T, pattern string) {
	long(t)
	out, e := child(t, nil, "", pattern, true, 20)
	expect(t, out, e, false, "PASS")
	if n := strings.Count(out, "--- PASS:"); n != 20 {
		t.Fatalf("only %d successful repetitions", n)
	}
	if strings.Contains(pattern, "Ablation") {
		re := regexp.MustCompile(`tier R ablation: [^\n]+`)
		hits := re.FindAllString(out, -1)
		if len(hits) != 20 {
			t.Fatal("missing ablation counts")
		}
		for _, h := range hits {
			if h != hits[0] {
				t.Fatalf("counts varied: %s vs %s", hits[0], h)
			}
		}
	}
}
func TestAdv_8_RepeatIsolation(t *testing.T) { repeat(t, "^TestTierRSessionStateNeverCrossesClients$") }
func TestAdv_8_RepeatAblation(t *testing.T) {
	repeat(t, "^TestTierRSessionAblationWithoutResetCrosses$")
}
func TestAdv_8_RepeatPolicies(t *testing.T) {
	repeat(t, "^TestTierRPoolSessionStateUnderEachReleasePolicy$")
}

func hashes(t *testing.T, group string, count int) {
	begin(t)
	re := regexp.MustCompile("(?m)^\\| `([^`]+)` \\| `([^`]+)` \\| `([a-f0-9]{40})` \\|")
	n := 0
	for _, m := range re.FindAllStringSubmatch(read(t, "agent_work/chunks/lease-lab-port/NOTES.md"), -1) {
		if !strings.HasPrefix(m[1], group) {
			continue
		}
		n++
		p := records + "/" + m[1]
		var b string
		if strings.HasSuffix(p, "lease-report.sh") {
			s, e := run(t, root, nil, "git", "show", "23221ba:"+p)
			if e != nil {
				t.Fatal(e)
			}
			b = s
		} else {
			b = read(t, p)
		}
		h := sha1.Sum(append([]byte(fmt.Sprintf("blob %d\x00", len(b))), []byte(b)...))
		if fmt.Sprintf("%x", h) != m[3] {
			t.Errorf("%s hash=%x want=%s", p, h, m[3])
		}
	}
	if n != count {
		t.Fatalf("checked %d records want %d", n, count)
	}
}
func TestAdv_9_NoteReviewHashes(t *testing.T)    { hashes(t, "lab-records/", 19) }
func TestAdv_9_SQLAndScriptHashes(t *testing.T)  { hashes(t, "report-sql/", 24) }
func TestAdv_9_IsolationOracleHash(t *testing.T) { hashes(t, "e7/", 1) }

var reportOnce sync.Once
var reportOut string
var reportErr error
var reportDir string

func report(t *testing.T) {
	begin(t)
	reportOnce.Do(func() {
		d, e := os.MkdirTemp(scratch, "report space '")
		if e != nil {
			t.Fatal(e)
		}
		reportDir = d
		os.MkdirAll(filepath.Join(d, "in"), 0755)
		out, e := run(t, root, nil, "tar", "-xzf", records+"/e7/tier-r-per-run.tar.gz", "-C", filepath.Join(d, "in"))
		if e != nil {
			t.Fatalf("unpack: %v %s", e, out)
		}
		reportOut, reportErr = run(t, d, nil, "bash", filepath.Join(root, records, "report-sql/lease-report.sh"), filepath.Join(d, "in/1-r6"), filepath.Join(d, "out"), "server_extra=0")
		save(t, reportOut)
	})
	if reportErr == nil || !strings.Contains(reportOut, "12 tables") {
		t.Fatalf("expected incomplete input with 12 tables, got %v %s", reportErr, reportOut)
	}
}
func TestAdv_10_RelocatedQuotedWorkingDirectory(t *testing.T) {
	report(t)
	b, e := os.ReadFile(filepath.Join(reportDir, "out/index.md"))
	if e != nil || !bytes.Contains(b, []byte("NOT READABLE")) {
		t.Fatalf("no incomplete-input warning: %v", e)
	}
}
func TestAdv_10_CalibrationByteEquality(t *testing.T) {
	report(t)
	b, e := os.ReadFile(filepath.Join(reportDir, "out/01-calibration.csv"))
	if e != nil {
		t.Fatal(e)
	}
	if string(b) != read(t, records+"/e7/r6/tables/01-calibration.csv") {
		t.Fatal("calibration differs byte for byte")
	}
}
func TestAdv_10_MissingInputsFailClosed(t *testing.T) {
	report(t)
	b, e := os.ReadFile(filepath.Join(reportDir, "out/00-validity.csv"))
	if e != nil {
		t.Fatal(e)
	}
	for _, s := range []string{"histograms.csv", "metrics.csv", "oracle.csv"} {
		if !bytes.Contains(b, []byte(s)) {
			t.Errorf("missing-input report omits %s", s)
		}
	}
}

func oracle(t *testing.T, variant int) {
	begin(t)
	d, e := os.MkdirTemp(scratch, "oracle-")
	if e != nil {
		t.Fatal(e)
	}
	defer os.RemoveAll(d)
	out, e := run(t, root, nil, "tar", "-xzf", records+"/e7/isolation/per-run.tar.gz", "-C", d)
	if e != nil {
		t.Fatalf("unpack %v %s", e, out)
	}
	sql := read(t, records+"/e7/isolation/e4.sql")
	if variant > 0 {
		files, _ := filepath.Glob(filepath.Join(d, "*", "oracle.csv"))
		if len(files) == 0 {
			t.Fatal("no oracle files")
		}
		b, e := os.ReadFile(files[0])
		if e != nil {
			t.Fatal(e)
		}
		if variant == 1 {
			lines := strings.Split(string(b), "\n")
			b = []byte(lines[0] + "\n")
		} else {
			b = []byte(strings.ReplaceAll(string(b), ",0,", ",-1,"))
		}
		// Empty ALL oracle files, or corrupt one catalog row; both must fail closed.
		if variant == 1 {
			for _, f := range files {
				os.WriteFile(f, b, 0644)
			}
		} else {
			os.WriteFile(files[0], b, 0644)
		}
	}
	q := "SET VARIABLE e4_root='" + strings.ReplaceAll(d, "'", "''") + "';\n" + sql
	p := filepath.Join(d, "probe.sql")
	os.WriteFile(p, []byte(q), 0644)
	out, e = run(t, root, nil, "duckdb", "-bail", "-f", p)
	save(t, out)
	if variant == 0 {
		if e != nil {
			t.Fatalf("oracle %v %s", e, out)
		}
		if out != read(t, records+"/e7/isolation/e4-oracle-check.txt") {
			t.Fatal("oracle output differs from record")
		}
	} else if e == nil {
		t.Fatal("invalid/empty oracle input accepted")
	}
}
func TestAdv_11_ExactOutput(t *testing.T)      { oracle(t, 0) }
func TestAdv_11_EmptyInputs(t *testing.T)      { oracle(t, 1) }
func TestAdv_11_UncheckedCatalog(t *testing.T) { oracle(t, 2) }

func TestAdv_12_SQLDiscoveryScope(t *testing.T) {
	begin(t)
	for _, p := range []string{"internal/obs/simrun/csvassert_test.go", "internal/obs/simrun/wiresql_test.go", "internal/obs/simrun/classifier_test.go"} {
		s := read(t, p)
		if strings.Contains(s, "lease-exp") || !strings.Contains(s, `"analysis"`) {
			t.Errorf("SQL discovery scope changed: %s", p)
		}
	}
}
func TestAdv_12_CSVDiscoveryScope(t *testing.T) {
	begin(t)
	s := read(t, "scripts/csv-assert.sh")
	if strings.Contains(s, "agent_work/chunks") {
		t.Fatal("G7 scans archived chunk data")
	}
	if !strings.Contains(s, "test/analysis") {
		t.Fatal("G7 analysis root no longer explicit")
	}
}
func TestAdv_12_SiteExcludesRecords(t *testing.T) {
	begin(t)
	d, e := os.MkdirTemp(scratch, "site-")
	if e != nil {
		t.Fatal(e)
	}
	defer os.RemoveAll(d)
	out, e := run(t, root, nil, "bash", "scripts/site.sh", d)
	save(t, out)
	if e != nil {
		t.Fatalf("site: %v %s", e, out)
	}
	filepath.WalkDir(d, func(p string, ent os.DirEntry, e error) error {
		if e != nil {
			t.Fatal(e)
		}
		if strings.Contains(p, "lab-records") || strings.Contains(p, "report-sql") {
			t.Errorf("archive published: %s", p)
		}
		return nil
	})
}
