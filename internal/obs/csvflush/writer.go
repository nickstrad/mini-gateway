package csvflush

import (
	"bytes"
	"cmp"
	"encoding/csv"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"

	"github.com/nickstrad/mini-gateway/internal/obs"
)

const (
	// AdapterName is the name under which Writer attaches to obs.Collector.
	AdapterName = "csv"
	// DefaultMaxBytes is the default 1 GiB cap on total bytes written to a run directory.
	DefaultMaxBytes = int64(1) << 30
	// DefaultQueueCap is the default async flush channel capacity.
	DefaultQueueCap = 64
	// MaxQueueCap clamps Config.QueueCap so the async queue stays bounded.
	MaxQueueCap = 4096
	// MaxTrackedRows caps persisted row keys kept for tenant-row persistence.
	MaxTrackedRows   = 1 << 20
	maxRunIDLen      = 200
	maxRunIDSegments = 8
)

// Sentinel errors returned by Writer setup, Flush, Close, and Err.
var (
	ErrDropped  = errors.New("csvflush: flush not written")
	ErrStopped  = errors.New("csvflush: writer stopped after the size cap or a write error")
	ErrClosed   = errors.New("csvflush: writer closed")
	ErrBadRunID = errors.New("csvflush: bad run id")
	ErrNoClock  = errors.New("csvflush: no clock")
)

// RunInfo supplies the static metadata recorded in run.csv on Close.
type RunInfo struct {
	RunID             string
	Scenario          string
	Tier              string
	Seed              int64
	Arm               string
	Instances         int64
	Build             string
	ConfigFingerprint string
}

type plant uint8

const (
	plantNone plant = iota
	plantDropLastBucket
	plantStaleValue
)

// Config configures a CSV flush Writer. Clock is required so the adapter stamps
// t_ns from the caller's clock (virtual time in the simulator, monotonic clock
// in a real process) while snapshots themselves remain timeless.
type Config struct {
	Dir      string
	Run      RunInfo
	MaxBytes int64
	Clock    func() int64
	Async    bool
	QueueCap int
	Strict   bool

	plant  plant
	create func(path string) (io.WriteCloser, error)
	gate   func()
}

type job struct {
	snaps []obs.InstanceSnapshot
	tNs   int64
	rows  int64
}

type rowKey struct {
	instance  string
	component string
	metric    string
	labels    obs.Labels
}

type tracked struct {
	key   rowKey
	point obs.Point
	hist  obs.HistPoint
	isH   bool
}

// Writer implements obs.Adapter and obs.FlushReporter, appending flushed
// snapshots to metrics.csv and histograms.csv and writing run.csv last on Close.
type Writer struct {
	cfg    Config
	runDir string

	queue chan job
	done  sync.WaitGroup

	closed      bool
	last        job
	lastDropped bool

	metrics io.WriteCloser
	hists   io.WriteCloser
	rows    []tracked
	prevVal []obs.Point
	stopped atomic.Bool

	flushes     atomic.Int64
	flushErrors atomic.Int64
	rowsDropped atomic.Int64
	bytes       atomic.Int64
	writeErr    atomic.Pointer[error]
}

// ValidRunID validates that id is a safe relative path of 1 to 8 segments using
// only [A-Za-z0-9._=-] and '/', with no empty, "." or ".." segments.
func ValidRunID(id string) error {
	if id == "" || len(id) > maxRunIDLen {
		return fmt.Errorf("%w: %q must be 1 to %d bytes", ErrBadRunID, id, maxRunIDLen)
	}
	segs := strings.Split(id, "/")
	if len(segs) > maxRunIDSegments {
		return fmt.Errorf("%w: %q has more than %d parts", ErrBadRunID, id, maxRunIDSegments)
	}
	for _, s := range segs {
		if s == "" || s == "." || s == ".." {
			return fmt.Errorf("%w: %q has an empty, . or .. part", ErrBadRunID, id)
		}
		for i := 0; i < len(s); i++ {
			c := s[i]
			if !(c >= 'a' && c <= 'z' || c >= 'A' && c <= 'Z' || c >= '0' && c <= '9' || c == '.' || c == '_' || c == '-' || c == '=') {
				return fmt.Errorf("%w: %q: only [A-Za-z0-9._=-] and / are allowed", ErrBadRunID, id)
			}
		}
	}
	return nil
}

// New creates a Writer for <cfg.Dir>/<cfg.Run.RunID>/, removing any leftover
// CSV files from an earlier run with the same id so reruns never mix files or
// leave a stale run.csv behind.
func New(cfg Config) (*Writer, error) {
	if err := ValidRunID(cfg.Run.RunID); err != nil {
		return nil, err
	}
	if cfg.Clock == nil {
		return nil, ErrNoClock
	}
	if cfg.MaxBytes <= 0 {
		cfg.MaxBytes = DefaultMaxBytes
	}
	if cfg.QueueCap <= 0 {
		cfg.QueueCap = DefaultQueueCap
	}
	cfg.QueueCap = min(cfg.QueueCap, MaxQueueCap)
	if cfg.Run.Instances <= 0 {
		cfg.Run.Instances = 1
	}
	if cfg.create == nil {
		cfg.create = createFile
	}
	w := &Writer{cfg: cfg, runDir: filepath.Join(cfg.Dir, filepath.FromSlash(cfg.Run.RunID))}
	if err := os.MkdirAll(w.runDir, 0o755); err != nil {
		return nil, err
	}
	for _, f := range []string{RunFile, MetricsFile, HistogramsFile} {
		if err := os.Remove(filepath.Join(w.runDir, f)); err != nil && !errors.Is(err, os.ErrNotExist) {
			return nil, err
		}
	}
	if cfg.Async {
		w.queue = make(chan job, cfg.QueueCap)
		w.done.Add(1)
		go w.loop()
	}
	return w, nil
}

func createFile(path string) (io.WriteCloser, error) {
	return os.OpenFile(path, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, 0o644)
}

// Name returns AdapterName ("csv").
func (w *Writer) Name() string { return AdapterName }

// RunDir returns the directory path holding this run's three CSV files.
func (w *Writer) RunDir() string { return w.runDir }

func (w *Writer) loop() {
	defer w.done.Done()
	for j := range w.queue {
		if w.cfg.gate != nil {
			w.cfg.gate()
		}
		w.write(j)
	}
}

// Export implements obs.Adapter by flushing a single instance snapshot.
func (w *Writer) Export(s obs.InstanceSnapshot) error { return w.Flush(s) }

// Flush stamps t_ns from Config.Clock, clones and sorts snaps by (Seq, Instance),
// and either writes them synchronously or enqueues them without blocking in
// Async mode (dropping and counting if the queue is full).
func (w *Writer) Flush(snaps ...obs.InstanceSnapshot) error {
	if w.closed {
		return ErrClosed
	}
	j := job{tNs: w.cfg.Clock()}
	for _, s := range snaps {
		j.snaps = append(j.snaps, s.Clone())
		j.rows += int64(len(s.Points))
		for _, h := range s.Hists {
			j.rows += int64(len(h.Buckets))
		}
	}
	slices.SortFunc(j.snaps, compareSnapshot)
	w.last = j
	w.lastDropped = false
	if !w.cfg.Async {
		return w.write(j)
	}
	select {
	case w.queue <- j:
		return nil
	default:
		w.lastDropped = true
		w.drop(j)
		return fmt.Errorf("%w: queue full (%d)", ErrDropped, w.cfg.QueueCap)
	}
}

func compareSnapshot(a, b obs.InstanceSnapshot) int {
	if a.Seq != b.Seq {
		return cmp.Compare(a.Seq, b.Seq)
	}
	return strings.Compare(a.Instance, b.Instance)
}

func (w *Writer) drop(j job) {
	w.flushErrors.Add(1)
	w.rowsDropped.Add(j.rows)
}

func (w *Writer) write(j job) error {
	if w.stopped.Load() {
		w.drop(j)
		return ErrStopped
	}
	mb, hb := w.render(j)
	// Account for pending CSV headers before opening files so flush 0 never exceeds MaxBytes.
	size := int64(len(mb)+len(hb)) + w.pendingHeaderBytes()
	if w.bytes.Load()+size > w.cfg.MaxBytes {
		w.stop(j, fmt.Errorf("%w: the flush of %d bytes would pass obs.csv.max_bytes %d", ErrDropped, size, w.cfg.MaxBytes))
		return ErrStopped
	}
	if err := w.open(); err != nil {
		w.stop(j, err)
		return ErrStopped
	}
	if err := writeAll(w.metrics, mb); err != nil {
		w.stop(j, err)
		return ErrStopped
	}
	if err := writeAll(w.hists, hb); err != nil {
		w.stop(j, err)
		return ErrStopped
	}
	w.bytes.Add(int64(len(mb) + len(hb)))
	w.flushes.Add(1)
	return nil
}

func (w *Writer) pendingHeaderBytes() int64 {
	if w.metrics != nil {
		return 0
	}
	return int64(len(headerBytes(MetricsHeader())) + len(headerBytes(HistogramsHeader())))
}

func writeAll(f io.Writer, b []byte) error {
	n, err := f.Write(b)
	if err == nil && n != len(b) {
		err = io.ErrShortWrite
	}
	return err
}

func (w *Writer) stop(j job, err error) {
	w.stopped.Store(true)
	w.writeErr.CompareAndSwap(nil, &err)
	w.drop(j)
}

func (w *Writer) open() error {
	if w.metrics != nil {
		return nil
	}
	m, err := w.cfg.create(filepath.Join(w.runDir, MetricsFile))
	if err != nil {
		return err
	}
	h, err := w.cfg.create(filepath.Join(w.runDir, HistogramsFile))
	if err != nil {
		m.Close()
		return err
	}
	w.metrics, w.hists = m, h
	hm := headerBytes(MetricsHeader())
	hh := headerBytes(HistogramsHeader())
	if err := writeAll(m, hm); err != nil {
		return err
	}
	if err := writeAll(h, hh); err != nil {
		return err
	}
	w.bytes.Add(int64(len(hm) + len(hh)))
	return nil
}

func headerBytes(cols []string) []byte {
	var b bytes.Buffer
	cw := csv.NewWriter(&b)
	_ = cw.Write(cols)
	cw.Flush()
	return b.Bytes()
}

func (w *Writer) render(j job) ([]byte, []byte) {
	var mb, hb bytes.Buffer
	mw, hw := csv.NewWriter(&mb), csv.NewWriter(&hb)
	run := w.cfg.Run.RunID
	ver := strconv.Itoa(SchemaVersion)
	t := strconv.FormatInt(j.tNs, 10)
	for _, s := range j.snaps {
		seq := strconv.FormatInt(s.Seq, 10)
		points, hists := w.persist(s)
		for i, p := range points {
			v := p.Value
			if w.cfg.plant == plantStaleValue && i < len(w.prevVal) {
				v = w.prevVal[i].Value
			}
			_ = mw.Write([]string{ver, run, seq, t, s.Instance, p.Component, p.Metric, p.Kind.String(),
				p.Labels.Tenant, p.Labels.Outcome, p.Labels.Reason, p.Labels.Mode, strconv.FormatInt(v, 10)})
		}
		w.prevVal = points
		for hi, h := range hists {
			buckets := h.Buckets
			if w.cfg.plant == plantDropLastBucket && hi == len(hists)-1 && len(buckets) > 0 {
				buckets = buckets[:len(buckets)-1]
			}
			for _, b := range buckets {
				// obs.Bucket.Hi is inclusive; CSV schema v1 writes bucket_hi exclusive (b.Hi+1).
				_ = hw.Write([]string{ver, run, seq, t, s.Instance, h.Component, h.Metric,
					h.Labels.Tenant, h.Labels.Outcome, h.Labels.Reason, h.Labels.Mode, string(h.Unit),
					strconv.FormatInt(b.Lo, 10), strconv.FormatInt(b.Hi+1, 10), strconv.FormatInt(b.Count, 10)})
			}
		}
	}
	mw.Flush()
	hw.Flush()
	return mb.Bytes(), hb.Bytes()
}

func compareKey(a, b rowKey) int {
	if c := strings.Compare(a.instance, b.instance); c != 0 {
		return c
	}
	if c := strings.Compare(a.component, b.component); c != 0 {
		return c
	}
	if c := strings.Compare(a.metric, b.metric); c != 0 {
		return c
	}
	return obs.CompareLabels(a.labels, b.labels)
}

// persist keeps every previously emitted row key for s.Instance in later
// flushes with its last value (generalizing tenant-row persistence) and re-sorts
// the flush into canonical order.
func (w *Writer) persist(s obs.InstanceSnapshot) ([]obs.Point, []obs.HistPoint) {
	points := slices.Clone(s.Points)
	hists := slices.Clone(s.Hists)
	for _, p := range s.Points {
		w.track(tracked{key: rowKey{s.Instance, p.Component, p.Metric, p.Labels}, point: p})
	}
	for _, h := range s.Hists {
		// Prefix histogram keys with \x00 so a histogram and a scalar metric cannot collide in w.rows.
		w.track(tracked{key: rowKey{s.Instance, h.Component, "\x00" + h.Metric, h.Labels}, hist: h, isH: true})
	}
	for _, r := range w.rows {
		if r.key.instance != s.Instance {
			continue
		}
		if r.isH {
			if _, ok := s.Hist(r.hist.Component, r.hist.Metric, r.hist.Labels); !ok {
				hists = append(hists, r.hist)
			}
		} else if _, ok := s.Point(r.point.Component, r.point.Metric, r.point.Labels); !ok {
			points = append(points, r.point)
		}
	}
	slices.SortFunc(points, obs.ComparePoint)
	slices.SortFunc(hists, obs.CompareHist)
	return points, hists
}

func (w *Writer) track(t tracked) {
	i, found := slices.BinarySearchFunc(w.rows, t.key, func(x tracked, k rowKey) int { return compareKey(x.key, k) })
	if found {
		w.rows[i] = t
		return
	}
	if len(w.rows) >= MaxTrackedRows {
		return
	}
	w.rows = slices.Insert(w.rows, i, t)
}

// Close drains any queued async flushes, writes the last snapshot if it had
// been dropped from a full queue (without decrementing drop counters), closes
// metrics.csv and histograms.csv, and writes run.csv last. If the writer
// stopped at MaxBytes or on a write error, run.csv is withheld so downstream
// assertions fail closed. When Config.Strict is true, Close returns Err().
func (w *Writer) Close() error {
	if w.closed {
		return ErrClosed
	}
	w.closed = true
	if w.cfg.Async {
		close(w.queue)
		w.done.Wait()
	}
	if w.lastDropped && !w.stopped.Load() {
		_ = w.write(w.last)
	}
	var err error
	if !w.stopped.Load() && w.bytes.Load()+w.pendingHeaderBytes() > w.cfg.MaxBytes {
		w.stopped.Store(true)
		w.flushErrors.Add(1)
		err = fmt.Errorf("%w: the headers would pass obs.csv.max_bytes %d", ErrDropped, w.cfg.MaxBytes)
	}
	if !w.stopped.Load() {
		if oerr := w.open(); oerr != nil {
			w.stopped.Store(true)
			err = oerr
		}
	}
	if w.metrics != nil {
		if cerr := errors.Join(w.metrics.Close(), w.hists.Close()); cerr != nil {
			w.stopped.Store(true)
			w.flushErrors.Add(1)
			err = errors.Join(err, cerr)
		}
	}
	err = errors.Join(err, w.finish())
	if err != nil {
		w.writeErr.CompareAndSwap(nil, &err)
	}
	if w.cfg.Strict {
		return w.Err()
	}
	return nil
}

func (w *Writer) finish() error {
	if w.stopped.Load() {
		return nil
	}
	r := w.cfg.Run
	var b bytes.Buffer
	cw := csv.NewWriter(&b)
	_ = cw.Write(RunHeader())
	_ = cw.Write([]string{strconv.Itoa(SchemaVersion), r.RunID, r.Scenario, r.Tier, strconv.FormatInt(r.Seed, 10), r.Arm,
		strconv.FormatInt(r.Instances, 10), r.Build, r.ConfigFingerprint, strconv.FormatInt(w.flushes.Load(), 10),
		strconv.FormatInt(w.cfg.Clock(), 10)})
	cw.Flush()
	if w.bytes.Load()+int64(b.Len()) > w.cfg.MaxBytes {
		w.stopped.Store(true)
		w.flushErrors.Add(1)
		return fmt.Errorf("%w: run.csv would pass obs.csv.max_bytes", ErrDropped)
	}
	f, err := w.cfg.create(filepath.Join(w.runDir, RunFile))
	if err != nil {
		w.flushErrors.Add(1)
		return err
	}
	if err := writeAll(f, b.Bytes()); err != nil {
		f.Close()
		w.flushErrors.Add(1)
		return err
	}
	w.bytes.Add(int64(b.Len()))
	return f.Close()
}

// FlushStats implements obs.FlushReporter using atomic loads so the collector
// or concurrent readers can inspect writer stats safely while Async mode runs.
func (w *Writer) FlushStats() obs.FlushStats {
	depth := int64(0)
	if w.queue != nil {
		depth = int64(len(w.queue))
	}
	return obs.FlushStats{Flushes: w.flushes.Load(), FlushErrors: w.flushErrors.Load(), RowsDropped: w.rowsDropped.Load(),
		BytesWritten: w.bytes.Load(), QueueDepth: depth}
}

// Err returns any write error or drop count recorded during the writer's lifetime.
func (w *Writer) Err() error {
	var errs []error
	if p := w.writeErr.Load(); p != nil {
		errs = append(errs, *p)
	}
	if n := w.flushErrors.Load(); n > 0 {
		errs = append(errs, fmt.Errorf("%w: %d flushes, %d rows dropped", ErrDropped, n, w.rowsDropped.Load()))
	}
	return errors.Join(errs...)
}
