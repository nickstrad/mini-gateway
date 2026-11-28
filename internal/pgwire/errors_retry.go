package pgwire

// ProxyRetry records whether the proxy itself may retry a failure that would
// otherwise be answered with a given error-contract row.
type ProxyRetry uint8

const (
	// ProxyRetryNever means the proxy never retries failures mapped to this row.
	ProxyRetryNever ProxyRetry = iota
	// ProxyRetryWhenNotReached means the proxy may retry once under
	// pre-forward-once only when the failure itself carries Reached == No.
	ProxyRetryWhenNotReached
)

var proxyRetryRows = [ErrRowCount]ProxyRetry{
	ErrRowLostAfterRead: ProxyRetryWhenNotReached,
}

// ProxyRetry reports whether the proxy itself may retry a failure mapped to id.
func (id ErrRowID) ProxyRetry() ProxyRetry {
	if id < ErrRowCount {
		return proxyRetryRows[id]
	}
	return ProxyRetryNever
}

// String renders r for the "Retried by the proxy?" column of docs/error-contract.md.
func (r ProxyRetry) String() string {
	if r == ProxyRetryWhenNotReached {
		return "yes, once, only when nothing of the request reached the database (its first write refused with zero bytes accepted), outside a transaction, with the breaker closed, before the request's deadline and within the retry budget; policy pre-forward-once only"
	}
	return "no"
}
