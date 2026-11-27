package pgwire

type ProxyRetry uint8

const (
	ProxyRetryNever ProxyRetry = iota
	ProxyRetryWhenNotReached
)

var proxyRetryRows = [ErrRowCount]ProxyRetry{
	ErrRowLostAfterRead: ProxyRetryWhenNotReached,
}

func (id ErrRowID) ProxyRetry() ProxyRetry {
	if id < ErrRowCount {
		return proxyRetryRows[id]
	}
	return ProxyRetryNever
}

func (r ProxyRetry) String() string {
	if r == ProxyRetryWhenNotReached {
		return "yes, once, only when nothing of the request reached the database (its first write refused with zero bytes accepted), outside a transaction, with the breaker closed, before the request's deadline and within the retry budget; policy pre-forward-once only"
	}
	return "no"
}
