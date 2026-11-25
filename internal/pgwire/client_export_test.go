package pgwire

// PlantEarlyBytes disables the "no bytes between S and HandshakeDone" guard
// for the planted-bug canary in external tests.
func PlantEarlyBytes(c *ClientConfig) { c.plantEarlyBytes = true }
