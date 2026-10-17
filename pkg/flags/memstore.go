package flags

import (
	"context"
	"sort"
	"sync"
)

type memStore struct {
	mu sync.Mutex
	m  map[string]Rule
}

// NewMemStore returns an in-memory Store seeded with rules. For tests.
func NewMemStore(rules ...Rule) Store {
	s := &memStore{m: map[string]Rule{}}
	for _, r := range rules {
		s.m[r.Key] = r
	}
	return s
}

func (s *memStore) List(context.Context) ([]Rule, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	out := make([]Rule, 0, len(s.m))
	for _, r := range s.m {
		out = append(out, r)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Key < out[j].Key })
	return out, nil
}

func (s *memStore) Save(_ context.Context, r Rule) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.m[r.Key] = r
	return nil
}
