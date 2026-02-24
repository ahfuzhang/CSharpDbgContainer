package debugadmin

import "sync"

type TraceStore struct {
	mu    sync.RWMutex
	items map[string]struct{}
	order []string
}

func NewTraceStore() *TraceStore {
	return &TraceStore{
		items: make(map[string]struct{}),
		order: make([]string, 0),
	}
}

func (s *TraceStore) Add(traceID string) {
	s.mu.Lock()
	if _, ok := s.items[traceID]; !ok {
		s.items[traceID] = struct{}{}
		s.order = append(s.order, traceID)
	}
	s.mu.Unlock()
}

func (s *TraceStore) Exists(traceID string) bool {
	s.mu.RLock()
	_, ok := s.items[traceID]
	s.mu.RUnlock()
	return ok
}

func (s *TraceStore) List() []string {
	s.mu.RLock()
	defer s.mu.RUnlock()
	items := make([]string, len(s.order))
	copy(items, s.order)
	return items
}
