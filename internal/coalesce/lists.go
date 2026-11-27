package coalesce

import (
	"slices"
	"strings"

	"github.com/nickstrad/mini-gateway/internal/work"
)

type noShareItem struct {
	key   string
	until Instant
}

type noShareList struct {
	ring  []noShareItem
	head  int
	n     int
	index map[string]int
}

func newNoShareList(capacity int) noShareList {
	return noShareList{ring: make([]noShareItem, capacity), index: make(map[string]int)}
}

func (l *noShareList) has(key string) bool {
	_, ok := l.index[key]
	return ok
}

func (l *noShareList) add(key string, until Instant) (evicted, renewed bool) {
	if l.has(key) {
		l.remove(key)
		renewed = true
	}
	if l.n == len(l.ring) {
		l.drop()
		evicted = true
	}
	l.ring[(l.head+l.n)%len(l.ring)] = noShareItem{key: key, until: until}
	l.index[key] = 1
	l.n++
	return evicted, renewed
}

func (l *noShareList) remove(key string) {
	for i := 0; i < l.n; i++ {
		p := (l.head + i) % len(l.ring)
		if l.ring[p].key != key {
			continue
		}
		for j := i; j < l.n-1; j++ {
			l.ring[(l.head+j)%len(l.ring)] = l.ring[(l.head+j+1)%len(l.ring)]
		}
		l.ring[(l.head+l.n-1)%len(l.ring)] = noShareItem{}
		l.n--
		delete(l.index, key)
		return
	}
}

func (l *noShareList) drop() {
	if l.n == 0 {
		return
	}
	it := l.ring[l.head]
	l.ring[l.head] = noShareItem{}
	l.head = (l.head + 1) % len(l.ring)
	l.n--
	delete(l.index, it.key)
}

func (l *noShareList) expire(now Instant) (string, bool) {
	if l.n == 0 || l.ring[l.head].until > now {
		return "", false
	}
	key := l.ring[l.head].key
	l.drop()
	return key, true
}

func (l *noShareList) next() (Instant, bool) {
	if l.n == 0 {
		return 0, false
	}
	return l.ring[l.head].until, true
}

type hotKey struct {
	key   string
	count int64
}

type hotKeys struct {
	max  int
	keys []hotKey
}

func (h *hotKeys) touch(key string) {
	if h.max == 0 {
		return
	}
	low := 0
	for i := range h.keys {
		if h.keys[i].key == key {
			h.keys[i].count++
			return
		}
		if h.keys[i].count < h.keys[low].count {
			low = i
		}
	}
	if len(h.keys) < h.max {
		h.keys = append(h.keys, hotKey{key: key, count: 1})
		return
	}
	h.keys[low] = hotKey{key: key, count: h.keys[low].count + 1}
}

func (h *hotKeys) snapshot() []HotKey {
	out := make([]HotKey, len(h.keys))
	for i, k := range h.keys {
		tenant, text, _ := strings.Cut(k.key, "\x00")
		out[i] = HotKey{Tenant: work.TenantID(tenant), Text: text, Count: k.count}
	}
	slices.SortFunc(out, func(a, b HotKey) int {
		if a.Count != b.Count {
			if a.Count > b.Count {
				return -1
			}
			return 1
		}
		if c := strings.Compare(string(a.Tenant), string(b.Tenant)); c != 0 {
			return c
		}
		return strings.Compare(a.Text, b.Text)
	})
	return out
}

type tenantStats struct {
	index map[work.TenantID]int
	hits  []TenantHits
}

func newTenantStats() tenantStats { return tenantStats{index: make(map[work.TenantID]int)} }

func (s *tenantStats) hit(t work.TenantID) {
	i, ok := s.index[t]
	if !ok {
		if len(s.hits) >= MaxTenantStats {
			t = OtherTenant
			i, ok = s.index[t]
		}
		if !ok {
			s.hits = append(s.hits, TenantHits{Tenant: t})
			i = len(s.hits) - 1
			s.index[t] = i
		}
	}
	s.hits[i].Hits++
}

func (s *tenantStats) snapshot() []TenantHits {
	out := slices.Clone(s.hits)
	slices.SortFunc(out, func(a, b TenantHits) int { return strings.Compare(string(a.Tenant), string(b.Tenant)) })
	return out
}
