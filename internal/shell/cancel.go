package shell

import (
	"context"
	"time"

	"github.com/nickstrad/mini-gateway/internal/proxy"
)

const (
	// closeWriteWait bounds TLS close-notify on abort so closing a blackholed socket never stalls.
	closeWriteWait = 100 * time.Millisecond
	maxCancels     = 1024
	cancelReadMax  = 64
)

// cancelRequest opens a short-lived plaintext cancel connection bounded by
// timeouts.cleanup and posts EvDBCancelDone once the server closes it or the
// deadline expires.
func (s *Shell) cancelRequest(id proxy.ConnID, data []byte) {
	if len(s.cancels) >= maxCancels || s.stopping() {
		if len(s.deferred) >= maxDeferred {
			s.stats.dropped.Add(1)
			return
		}
		s.deferred = append(s.deferred, proxy.Event{Kind: proxy.EvDBCancelDone, Conn: id})
		return
	}
	ctx, stop := context.WithTimeout(context.Background(), s.cfg.Timeouts.Cleanup)
	s.cancels[id] = stop
	msg := append([]byte(nil), data...)
	s.spawn(func() {
		defer stop()
		s.sendCancel(ctx, msg)
		s.post(envelope{ev: proxy.Event{Kind: proxy.EvDBCancelDone, Conn: id}})
	}, nil)
}

func (s *Shell) sendCancel(ctx context.Context, msg []byte) {
	nc, err := s.cfg.Dial(ctx, s.cfg.DBAddress)
	if err != nil {
		return
	}
	defer nc.Close()
	if dl, ok := ctx.Deadline(); ok {
		_ = nc.SetDeadline(dl)
	}
	stop := context.AfterFunc(ctx, func() { _ = nc.SetDeadline(time.Now()) })
	defer stop()
	if _, err := nc.Write(msg); err != nil {
		return
	}
	var buf [cancelReadMax]byte
	for {
		if _, err := nc.Read(buf[:]); err != nil {
			return
		}
	}
}
