package csvflush

const SchemaVersion = 1

const (
	MetricsFile    = "metrics.csv"
	HistogramsFile = "histograms.csv"
	RunFile        = "run.csv"
)

const (
	TierSim   = "S"
	TierReal  = "R"
	TierBench = "bench"
)

type Column struct {
	Name    string
	Type    string
	Meaning string
}

func MetricsColumns() []Column {
	return []Column{
		{"schema_version", "integer", "Always 1 in this version."},
		{"run_id", "text", "Set by whoever starts the run; simulator runs use <scenario>/<seed>/<arm>; also the run directory below obs.csv.dir."},
		{"flush_seq", "integer", "Publish sequence number of the snapshot, from 0."},
		{"t_ns", "integer", "Nanoseconds since the run began on the driver's clock, stamped by the adapter."},
		{"instance", "text", "Instance id."},
		{"component", "text", "Component that exported the value."},
		{"metric", "text", "A metric declared in the registry below."},
		{"kind", "text", "counter (cumulative, never decreases) or gauge (value at the snapshot)."},
		{"tenant", "text", "Tenant label; empty when the metric has none. Not bounded: every tenant has its own rows."},
		{"outcome", "text", "Outcome label; empty when the metric has none."},
		{"reason", "text", "Reason label; empty when the metric has none."},
		{"mode", "text", "Mode label; empty when the metric has none."},
		{"value", "integer", "The value. Integers only."},
	}
}

func HistogramsColumns() []Column {
	return []Column{
		{"schema_version", "integer", "Always 1 in this version."},
		{"run_id", "text", "As in metrics.csv."},
		{"flush_seq", "integer", "As in metrics.csv."},
		{"t_ns", "integer", "As in metrics.csv."},
		{"instance", "text", "As in metrics.csv."},
		{"component", "text", "As in metrics.csv."},
		{"metric", "text", "A histogram declared in the registry below."},
		{"tenant", "text", "As in metrics.csv."},
		{"outcome", "text", "As in metrics.csv."},
		{"reason", "text", "As in metrics.csv."},
		{"mode", "text", "As in metrics.csv."},
		{"unit", "text", "ns, bytes or count."},
		{"bucket_lo", "integer", "Lowest value of the bucket, inclusive."},
		{"bucket_hi", "integer", "Upper bound of the bucket, exclusive."},
		{"count", "integer", "Values recorded in the bucket since the start, cumulative. Only non-empty buckets have rows."},
	}
}

func RunColumns() []Column {
	return []Column{
		{"schema_version", "integer", "Always 1 in this version."},
		{"run_id", "text", "As in metrics.csv."},
		{"scenario", "text", "Scenario or benchmark name."},
		{"tier", "text", "S (simulator), R (real) or bench."},
		{"seed", "integer", "Seed of the run; 0 when it has none."},
		{"arm", "text", "Arm of the run, for example base or ablation."},
		{"instances", "integer", "Instances that flushed into this directory."},
		{"build", "text", "Build id."},
		{"config_fingerprint", "text", "Configuration fingerprint."},
		{"flushes", "integer", "Flushes written."},
		{"duration_ns", "integer", "Nanoseconds from the start of the run to the final flush, on the driver's clock."},
	}
}

func names(cs []Column) []string {
	out := make([]string, len(cs))
	for i, c := range cs {
		out[i] = c.Name
	}
	return out
}

func MetricsHeader() []string    { return names(MetricsColumns()) }
func HistogramsHeader() []string { return names(HistogramsColumns()) }
func RunHeader() []string        { return names(RunColumns()) }
