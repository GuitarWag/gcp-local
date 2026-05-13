package state

import (
	"strings"
	"sync"
)

type Memory struct {
	mu   sync.RWMutex
	data map[string]map[string][]byte
}

func NewMemory() *Memory {
	return &Memory{data: map[string]map[string][]byte{}}
}

func (m *Memory) ns(namespace string) map[string][]byte {
	if _, ok := m.data[namespace]; !ok {
		m.data[namespace] = map[string][]byte{}
	}
	return m.data[namespace]
}

func (m *Memory) Get(namespace, key string) ([]byte, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()
	bucket, ok := m.data[namespace]
	if !ok {
		return nil, ErrNotFound
	}
	v, ok := bucket[key]
	if !ok {
		return nil, ErrNotFound
	}
	out := make([]byte, len(v))
	copy(out, v)
	return out, nil
}

func (m *Memory) Put(namespace, key string, value []byte) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	b := m.ns(namespace)
	cp := make([]byte, len(value))
	copy(cp, value)
	b[key] = cp
	return nil
}

func (m *Memory) Delete(namespace, key string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	b, ok := m.data[namespace]
	if !ok {
		return ErrNotFound
	}
	if _, ok := b[key]; !ok {
		return ErrNotFound
	}
	delete(b, key)
	return nil
}

func (m *Memory) List(namespace, prefix string) (map[string][]byte, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()
	out := map[string][]byte{}
	b, ok := m.data[namespace]
	if !ok {
		return out, nil
	}
	for k, v := range b {
		if prefix == "" || strings.HasPrefix(k, prefix) {
			cp := make([]byte, len(v))
			copy(cp, v)
			out[k] = cp
		}
	}
	return out, nil
}

func (m *Memory) Close() error { return nil }
