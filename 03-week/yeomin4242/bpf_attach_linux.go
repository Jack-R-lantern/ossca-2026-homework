//go:build linux

package main

import (
	"fmt"
	"net"
)

func (m *bpfManager) Attach(ifName string) error {
	m.mu.Lock()
	defer m.mu.Unlock()

	iface, err := net.InterfaceByName(ifName)
	if err != nil {
		return fmt.Errorf("lookup interface %q: %w", ifName, err)
	}

	if existing, ok := m.attached[ifName]; ok && existing.ifIndex == iface.Index {
		if err := existing.verifyAttached(ifName); err == nil {
			return nil
		}

		existing.Close()
		delete(m.attached, ifName)
	}

	target, err := loadAndAttachXDP(iface.Index)
	if err != nil {
		return fmt.Errorf("attach XDP program to %q: %w", ifName, err)
	}

	if old := m.attached[ifName]; old != nil {
		old.Close()
	}

	m.attached[ifName] = target
	return nil
}
