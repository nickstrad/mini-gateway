package shell

import (
	"bytes"
	"context"
	"errors"
	"io"
	"log"
	"net/http"
	"strconv"
	"time"

	"github.com/nickstrad/mini-gateway/internal/obs"
	"github.com/nickstrad/mini-gateway/internal/obs/csvflush"
	"github.com/nickstrad/mini-gateway/internal/obs/jsonstats"
	"github.com/nickstrad/mini-gateway/internal/obs/promtext"
)

const (
	CheckListener      = "listener"
	PromContentType    = "text/plain; version=0.0.4"
	jsonContentType    = "application/json"
	notPublishedYet    = "nothing published yet"
	listenerUpReason   = "accepting clients"
	listenerDownReason = "the client listener is down"
)

func (s *Shell) wireObs() error {
	o := s.cfg.Obs
	s.collector = obs.NewCollector(obs.Config{Instance: o.Instance, Build: o.Build, ConfigFingerprint: o.ConfigFingerprint, Detail: o.Detail})
	if s.cfg.Sources != nil {
		if err := s.cfg.Sources(s.collector, s.Snapshot); err != nil {
			return err
		}
	}
	if err := s.health.Add(obs.CollectorRule()); err != nil {
		return err
	}
	for _, r := range s.cfg.HealthRules {
		if err := s.health.Add(r); err != nil {
			return errors.Join(err, errors.New("shell: health rule "+strconv.Quote(r.Name)))
		}
	}
	s.json = jsonstats.New(nil)
	s.prom = promtext.New(s.collector.Registry(), nil, promtext.Config{})
	adapters := []obs.Adapter{s.json, s.prom}
	if o.CSVDir != "" {
		w, err := csvflush.New(csvflush.Config{Dir: o.CSVDir, MaxBytes: o.CSVMaxBytes, Async: true,
			Clock: func() int64 { return time.Now().UnixNano() },
			Run: csvflush.RunInfo{RunID: o.Instance + "/" + strconv.FormatInt(s.base.Unix(), 10), Tier: csvflush.TierReal,
				Instances: 1, Build: o.Build, ConfigFingerprint: o.ConfigFingerprint}})
		if err != nil {
			return err
		}
		s.csv = w
		adapters = append(adapters, w)
	}
	for _, a := range adapters {
		if err := s.collector.Attach(a); err != nil {
			return errors.Join(err, s.closeObs())
		}
	}
	return nil
}

func (s *Shell) CSVRunDir() string {
	if s.csv == nil {
		return ""
	}
	return s.csv.RunDir()
}

func (s *Shell) closeObs() error {
	if s.csv == nil {
		return nil
	}
	return s.csv.Close()
}

func (s *Shell) publish() {
	_, _ = s.collector.Publish()
	s.pub.Store(&published{seq: s.collector.Seq(), health: s.collector.Health(&s.health), stats: bytes.Clone(s.json.Last()),
		metrics: bytes.Clone(s.prom.Last())})
}

func (s *Shell) Health() obs.Health {
	var h obs.Health
	if p := s.pub.Load(); p != nil {
		h.State = p.health.State
		h.Checks = append(h.Checks, p.health.Checks...)
	} else {
		h.State = obs.StateDegraded
		h.Checks = append(h.Checks, obs.CheckResult{Name: obs.CheckCollector, State: obs.StateDegraded, Reason: notPublishedYet})
	}
	l := obs.CheckResult{Name: CheckListener, State: obs.StateOK, Reason: listenerUpReason}
	if !s.listenerUp.Load() {
		l.State, l.Reason = obs.StateFailed, listenerDownReason
	}
	h.Checks = append(h.Checks, l)
	h.State = max(h.State, l.State)
	return h
}

func (s *Shell) startAdmin() {
	mux := http.NewServeMux()
	mux.HandleFunc("/healthz", s.serveHealth)
	mux.HandleFunc("/stats", s.serveStats)
	mux.HandleFunc("/metrics", s.serveMetrics)
	s.admin = &http.Server{Handler: mux, ReadHeaderTimeout: adminHeaderTimeout, WriteTimeout: adminWriteTimeout,
		IdleTimeout: adminIdleTimeout, MaxHeaderBytes: adminMaxHeaderBytes, ErrorLog: log.New(io.Discard, "", 0)}
	srv, ln := s.admin, s.cfg.AdminListener
	s.spawn(func() { _ = srv.Serve(ln) }, nil)
}

func (s *Shell) stopAdmin() {
	if s.admin == nil {
		if s.cfg.AdminListener != nil {
			_ = s.cfg.AdminListener.Close()
		}
		return
	}
	ctx, cancel := context.WithTimeout(context.Background(), adminShutdownTimeout)
	defer cancel()
	if err := s.admin.Shutdown(ctx); err != nil {
		_ = s.admin.Close()
	}
}

func (s *Shell) admitRequest(w http.ResponseWriter, r *http.Request) bool {
	s.stats.adminRequests.Add(1)
	if r.Method != http.MethodGet && r.Method != http.MethodHead {
		w.Header().Set("Allow", "GET, HEAD")
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return false
	}
	return true
}

func (s *Shell) serveHealth(w http.ResponseWriter, r *http.Request) {
	if !s.admitRequest(w, r) {
		return
	}
	h := s.Health()
	code := http.StatusOK
	if h.State == obs.StateFailed {
		code = http.StatusServiceUnavailable
	}
	reply(w, code, jsonContentType, h.AppendJSON(nil))
}

func (s *Shell) serveStats(w http.ResponseWriter, r *http.Request) {
	if !s.admitRequest(w, r) {
		return
	}
	p := s.pub.Load()
	if p == nil || p.stats == nil {
		http.Error(w, notPublishedYet, http.StatusServiceUnavailable)
		return
	}
	reply(w, http.StatusOK, jsonContentType, p.stats)
}

func (s *Shell) serveMetrics(w http.ResponseWriter, r *http.Request) {
	if !s.admitRequest(w, r) {
		return
	}
	p := s.pub.Load()
	if p == nil || p.metrics == nil {
		http.Error(w, notPublishedYet, http.StatusServiceUnavailable)
		return
	}
	reply(w, http.StatusOK, PromContentType, p.metrics)
}

func reply(w http.ResponseWriter, code int, contentType string, body []byte) {
	w.Header().Set("Content-Type", contentType)
	w.Header().Set("Content-Length", strconv.Itoa(len(body)))
	w.WriteHeader(code)
	_, _ = w.Write(body)
}
