package proxy

func (e *Engine) DropFollowerEntryForTest() bool {
	for _, c := range e.clientList {
		if c.phase == cpCoalesced && e.followers[c.wid] == c {
			delete(e.followers, c.wid)
			return true
		}
	}
	return false
}

func (e *Engine) MisplaceOwnerForTest() bool {
	for _, c := range e.clientList {
		if c.fl != 0 && c.phase != cpCoalesced && c.req != nil {
			c.req.fl++
			return true
		}
	}
	return false
}
