package store

import "sync"

type Ring[T any] struct {
	mu     sync.RWMutex
	values []T
	start  int
	count  int
}

func NewRing[T any](size int) *Ring[T] {
	if size <= 0 {
		size = 1
	}
	return &Ring[T]{values: make([]T, size)}
}

func (r *Ring[T]) Add(v T) {
	r.mu.Lock()
	defer r.mu.Unlock()

	if r.count < len(r.values) {
		r.values[(r.start+r.count)%len(r.values)] = v
		r.count++
		return
	}

	r.values[r.start] = v
	r.start = (r.start + 1) % len(r.values)
}

func (r *Ring[T]) All() []T {
	r.mu.RLock()
	defer r.mu.RUnlock()

	out := make([]T, 0, r.count)
	for i := 0; i < r.count; i++ {
		out = append(out, r.values[(r.start+i)%len(r.values)])
	}
	return out
}
