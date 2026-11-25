package obs

const (
	MetricPublishes          = "publishes"
	MetricExportErrors       = "export_errors"
	MetricEmitErrors         = "emit_errors"
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
		CounterDesc(MetricExportErrors, LabelReason, "Exports an adapter failed; reason is the adapter name. Never propagated to the collector."),
		CounterDesc(MetricEmitErrors, 0, "Publishes whose sources emitted an undeclared, mistyped, duplicate or missing metric.").AsHeadline(),
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
	e.Counter(MetricPublishes, Labels{}, c.seq+1)
	e.Counter(MetricEmitErrors, Labels{}, c.emitErrors)
	e.Gauge(MetricSources, Labels{}, int64(len(c.sources)))
	e.Gauge(MetricAdapters, Labels{}, int64(len(c.adapters)))
	var fs FlushStats
	tracked := int64(0)
	for _, s := range c.adapters {
		e.Counter(MetricExportErrors, Labels{Reason: s.a.Name()}, s.errors)
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
