package pgwire

import (
	"strings"
	"testing"

	"github.com/nickstrad/mini-gateway/internal/work"
)

// TestEveryErrorRowStatesWhetherTheProxyRetriesIt proves docs/error-contract.md
// records ProxyRetry on every row and forbids retries on all non-eligible rows.
func TestEveryErrorRowStatesWhetherTheProxyRetriesIt(t *testing.T) {
	doc := ErrorRowsMarkdown()
	for id := ErrRowID(0); id < ErrRowCount; id++ {
		r := id.Row()
		line := ""
		for _, l := range strings.Split(doc, "\n") {
			if strings.HasPrefix(l, "| `"+r.ID+"` |") {
				line = l
			}
		}
		want := id.ProxyRetry().String()
		if !strings.Contains(line, "| "+want+" |") {
			t.Errorf("row %s does not state %q: %s", r.ID, want, line)
		}
		if id.ProxyRetry() == ProxyRetryNever {
			continue
		}
		if r.Refusal != work.RefusalNone || r.Reply != ErrorRowError || r.Conn != ErrorRowConnUsable || r.WriteApplied != work.No {
			t.Errorf("row %s is retryable by the proxy but is not a usable-connection loss with no write applied", r.ID)
		}
	}
	for _, id := range []ErrRowID{ErrRowLostInTransaction, ErrRowLostMidStatementInTransaction, ErrRowLostAfterWrite, ErrRowPartialReply,
		ErrRowIdleInTransaction, ErrRowShed, ErrRowBreakerOpen, ErrRowCancelledQueued, ErrRowPassThrough} {
		if id.ProxyRetry() != ProxyRetryNever {
			t.Errorf("row %s must never be retried by the proxy", id.Row().ID)
		}
	}
}
