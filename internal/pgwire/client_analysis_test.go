package pgwire_test

import (
	"bytes"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/nickstrad/mini-gateway/internal/pgwire"
)

func requireAnalysisFile(t *testing.T, scenario, file string) {
	t.Helper()
	path := analysisPath(scenario, file)
	b, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("%s: %v", path, err)
	}
	if len(b) == 0 {
		t.Fatalf("%s is empty", path)
	}
}

func analysisPath(scenario, file string) string {
	return filepath.Join("..", "..", "test", "analysis", scenario, file)
}

const csvHeader = "schema_version,run_id,flush_seq,t_ns,instance,component,metric,kind,tenant,outcome,reason,mode,value\n"

type csvRows struct {
	b   strings.Builder
	run string
}

func (c *csvRows) add(component, metric, outcome, reason, mode string, v int64) {
	fmt.Fprintf(&c.b, "1,%s,0,0,i0,%s,%s,counter,,%s,%s,%s,%d\n", c.run, component, metric, outcome, reason, mode, v)
}

func flushWire(r wireResult, run string) string {
	c := &csvRows{run: run}
	c.b.WriteString(csvHeader)
	s := r.final
	c.add("wire", "client_startups_begun", "", "", "", s.Client.StartupsBegun)
	for i, v := range s.Client.StartupsFailed {
		c.add("wire", "client_startups_failed", "", pgwire.Reason(i).String(), "", v)
	}
	for i, v := range s.Database.StartupsFailed {
		c.add("wire", "database_startups_failed", "", pgwire.Class(i).String(), "", v)
	}
	for k := range s.Client.Requests {
		for a, v := range s.Client.Requests[k] {
			c.add("wire", "client_prestartup_requests", pgwire.Answer(a).String(), "", pgwire.RequestKind(k).String(), v)
		}
	}
	for a, v := range s.Database.TLSAnswers {
		c.add("wire", "database_tls_answers", pgwire.Answer(a).String(), "", "", v)
	}
	st := r.wl.Total.Startup
	c.add("workload", "startup_tls_requests", "", "", "", st.TLSRequests)
	c.add("workload", "startup_tls_answered", "S", "", "", st.TLSAnsweredYes)
	c.add("workload", "startup_tls_answered", "N", "", "", st.TLSAnsweredNo)
	c.add("fakepg", "tls_answered", "S", "", "", r.auth.TLSAnsweredYes)
	c.add("fakepg", "tls_answered", "N", "", "", r.auth.TLSAnsweredNo)
	return c.b.String()
}

func runDuckDB(t *testing.T, bin, dir, sqlFile string) string {
	t.Helper()
	sql, err := os.ReadFile(sqlFile)
	if err != nil {
		t.Fatal(err)
	}
	cmd := exec.Command(bin, "-csv", "-noheader", "-c", string(sql))
	cmd.Dir = dir
	var out, stderr bytes.Buffer
	cmd.Stdout, cmd.Stderr = &out, &stderr
	if err := cmd.Run(); err != nil {
		t.Fatalf("duckdb %s: %v\n%s", sqlFile, err, stderr.String())
	}
	return strings.TrimSpace(out.String())
}

func TestWireAnalysisSQL(t *testing.T) {
	if os.Getenv("MG_DUCKDB") != "1" {
		t.Skip("set MG_DUCKDB=1 (the gate extra does) to run the SQL files with DuckDB")
	}
	bin, err := exec.LookPath("duckdb")
	if err != nil {
		bin = "/root/.local/bin/duckdb"
	}
	for _, sc := range wireScenarios() {
		t.Run(sc.name, func(t *testing.T) {
			r := runWire(t, 1, sc, "")
			if err := sc.verify(r); err != nil {
				t.Fatal(err)
			}
			dir := t.TempDir()
			csv := flushWire(r, sc.name+"/1/main")
			if err := os.WriteFile(filepath.Join(dir, "metrics.csv"), []byte(csv), 0o644); err != nil {
				t.Fatal(err)
			}
			for _, f := range []string{"startup-failures.sql", "tls-requests.sql"} {
				if out := runDuckDB(t, bin, dir, analysisPath(sc.name, f)); out != "" {
					t.Fatalf("%s: violations:\n%s", f, out)
				}
			}
			bad := strings.Replace(csv, ",wire,client_startups_failed,counter,,,deadline,,", ",wire,client_startups_failed,counter,,,deadline,,9", 1)
			bad = strings.Replace(bad, ",workload,startup_tls_requests,counter,,,,,", ",workload,startup_tls_requests,counter,,,,,7", 1)
			if err := os.WriteFile(filepath.Join(dir, "metrics.csv"), []byte(bad), 0o644); err != nil {
				t.Fatal(err)
			}
			for _, f := range []string{"startup-failures.sql", "tls-requests.sql"} {
				if out := runDuckDB(t, bin, dir, analysisPath(sc.name, f)); out == "" {
					t.Fatalf("%s: a planted wrong number was not caught", f)
				}
			}
		})
	}
}
