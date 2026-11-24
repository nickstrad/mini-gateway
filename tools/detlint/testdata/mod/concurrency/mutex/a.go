package mutex

import "sync"

type S struct {
	mu sync.Mutex
	n  int
}

func (s *S) Inc() {
	s.mu.Lock()
	s.n++
	s.mu.Unlock()
}
