//go:build linux

package main

import (
	"sync"
)

type bpfManager struct {
	mu       sync.Mutex
	attached map[string]*attachedInterface
}

func newBPFManager() *bpfManager {
	return &bpfManager{
		attached: make(map[string]*attachedInterface),
	}
}

func (m *bpfManager) Close() {
	m.mu.Lock()
	defer m.mu.Unlock()

	for ifName, target := range m.attached {
		target.Close()
		delete(m.attached, ifName)
	}
}
