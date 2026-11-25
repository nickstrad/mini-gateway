package obs

const (
	MetricPublishes          = "publishes"
	MetricExportErrors       = "export_errors"
	MetricEmitErrors         = "emit_errors"
	MetricAdapterFailing     = "adapter_failing"
	MetricEmitFailing        = "emit_failing"
	MetricSources            = "sources"
	MetricAdapters           = "adapters"
	MetricCSVFlushes         = "csv_flushes"
	MetricCSVFlushErrors     = "csv_flush_errors"
	MetricCSVRowsDropped     = "csv_rows_dropped"
	MetricCSVBytesWritten    = "csv_bytes_written"
	MetricCSVQueueDepth      = "csv_queue_depth"
	MetricPromTenantsTracked = "prom_tenants_tracked"
)

func SelfMetrics() []MetricDesc {
	return []MetricDesc{
		CounterDesc(MetricPublishes, 0, "Snapshots published by the collector, this one included.").AsHeadline(),
		CounterDesc(MetricExportErrors, LabelReason, "Exports an adapter failed; reason is the adapter name, one row per attached adapter. Never propagated to the collector.").WithOpenLabels(),
		CounterDesc(MetricEmitErrors, 0, "Publishes whose sources emitted an undeclared, mistyped, duplicate or missing metric.").AsHeadline(),
		GaugeDesc(MetricAdapterFailing, LabelReason, "1 while the adapter's latest export failed, else 0; reason is the adapter name.").WithOpenLabels(),
		GaugeDesc(MetricEmitFailing, 0, "1 when the latest publish had metric errors, else 0."),
		GaugeDesc(MetricSources, 0, "Sources registered with the collector."),
		GaugeDesc(MetricAdapters, 0, "Adapters attached to the collector."),
		CounterDesc(MetricCSVFlushes, 0, "CSV flushes written to disk.").AsHeadline(),
		CounterDesc(MetricCSVFlushErrors, 0, "CSV flushes not written: size cap reached, write error or full queue.").AsHeadline(),
		CounterDesc(MetricCSVRowsDropped, 0, "CSV rows not written because their flush was not written.").AsHeadline(),
		GaugeDesc(MetricCSVBytesWritten, 0, "Bytes written to the CSV run directory.").WithUnit(UnitBytes),
		GaugeDesc(MetricCSVQueueDepth, 0, "Flushes waiting in the CSV writer queue (real-process mode)."),
		GaugeDesc(MetricPromTenantsTracked, 0, "Tenants that hold a tenant label value in the Prometheus text adapter."),
	}
}

func (c *Collector) selfSource() Source {
	return Source{Component: ComponentObs, Metrics: SelfMetrics(), Collect: c.emitSelf}
}

func (c *Collector) emitSelf(e *Emitter) {
	c.emitSelfAt(e, c.seq+1)
}

func (c *Collector) emitSelfAt(e *Emitter, publishes int64) {
	e.Counter(MetricPublishes, Labels{}, publishes)
	e.Counter(MetricEmitErrors, Labels{}, c.emitErrors)
	e.Gauge(MetricEmitFailing, Labels{}, boolValue(c.lastEmit != nil))
	e.Gauge(MetricSources, Labels{}, int64(len(c.sources)))
	e.Gauge(MetricAdapters, Labels{}, int64(len(c.adapters)))
	var fs FlushStats
	tracked := int64(0)
	for _, s := range c.adapters {
		e.Counter(MetricExportErrors, Labels{Reason: s.a.Name()}, s.errors)
		e.Gauge(MetricAdapterFailing, Labels{Reason: s.a.Name()}, boolValue(s.failed))
		if r, ok := s.a.(FlushReporter); ok {
			x := r.FlushStats()
			fs.Flushes += x.Flushes
			fs.FlushErrors += x.FlushErrors
			fs.RowsDropped += x.RowsDropped
			fs.BytesWritten += x.BytesWritten
			fs.QueueDepth += x.QueueDepth
		}
		if r, ok := s.a.(TenantReporter); ok {
			tracked += r.TenantsTracked()
		}
	}
	e.Counter(MetricCSVFlushes, Labels{}, fs.Flushes)
	e.Counter(MetricCSVFlushErrors, Labels{}, fs.FlushErrors)
	e.Counter(MetricCSVRowsDropped, Labels{}, fs.RowsDropped)
	e.Gauge(MetricCSVBytesWritten, Labels{}, fs.BytesWritten)
	e.Gauge(MetricCSVQueueDepth, Labels{}, fs.QueueDepth)
	e.Gauge(MetricPromTenantsTracked, Labels{}, tracked)
}

func boolValue(b bool) int64 {
	if b {
		return 1
	}
	return 0
}

func (c *Collector) selfView() InstanceSnapshot {
	e := Emitter{reg: &c.reg, component: ComponentObs, detail: c.cfg.Detail}
	c.emitSelfAt(&e, c.seq)
	e.finish()
	out := c.last
	out.Points = make([]Point, 0, len(c.last.Points)+len(e.points))
	for _, p := range c.last.Points {
		if p.Component != ComponentObs {
			out.Points = append(out.Points, p)
		}
	}
	out.Points = append(out.Points, e.points...)
	out.Sort()
	return out
}
